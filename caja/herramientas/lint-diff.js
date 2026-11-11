#!/usr/bin/env node
// Lint que solo falla por errores en lineas que la rama agrego o cambio.
// La deuda vieja de un archivo tocado no frena el PR, y un error nuevo si.
// Uso (adentro de la caja, parado en el repo): node /opt/caja/lint-diff.js origin/qa
const { execFileSync } = require('child_process');
const path = require('path');

const base = process.argv[2] || 'origin/qa';
const git = (...a) => execFileSync('git', a, { encoding: 'utf8', maxBuffer: 64 * 1024 * 1024 });

const archivos = git('diff', '--name-only', '--diff-filter=ACMR', base, '--', '*.ts')
  .split('\n').filter((f) => f && !/^(frontend|cli)\//.test(f));
if (!archivos.length) {
  console.log('lint: el diff no toca .ts de backend');
  process.exit(0);
}

// Lineas nuevas por archivo, leidas de los hunks de un diff sin contexto.
const nuevas = {};
let actual = null;
for (const l of git('diff', '-U0', base, '--', ...archivos).split('\n')) {
  if (l.startsWith('+++ b/')) { actual = l.slice(6); nuevas[actual] = new Set(); continue; }
  const m = /^@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@/.exec(l);
  if (m && actual) {
    const desde = Number(m[1]); const cuantas = m[2] === undefined ? 1 : Number(m[2]);
    for (let i = 0; i < cuantas; i++) nuevas[actual].add(desde + i);
  }
}

let crudo;
try {
  crudo = execFileSync('npx', ['eslint', '-f', 'json', ...archivos], { encoding: 'utf8', maxBuffer: 256 * 1024 * 1024 });
} catch (e) {
  crudo = e.stdout; // eslint sale 1 cuando hay errores; el JSON viene igual
  if (!crudo) { console.error(e.stderr || String(e)); process.exit(2); }
}

let propios = 0; let viejos = 0; const fuera = [];
for (const r of JSON.parse(crudo)) {
  const rel = path.relative(process.cwd(), r.filePath);
  for (const m of r.messages) {
    if (m.severity !== 2) continue;
    if (m.line === undefined || /TSConfig does not include this file/.test(m.message)) { fuera.push(rel); continue; }
    if ((nuevas[rel] || new Set()).has(m.line)) {
      propios++;
      console.log(`${rel}:${m.line}:${m.column}  ${m.message}  ${m.ruleId || ''}`);
    } else viejos++;
  }
}
if (fuera.length) console.log(`lint: ${[...new Set(fuera)].length} archivo(s) fuera del tsconfig de eslint, sin revisar: ${[...new Set(fuera)].join(', ')}`);
console.log(`lint: ${propios} error(es) en lineas de esta rama; ${viejos} de deuda previa en archivos tocados (no frenan)`);
process.exit(propios ? 1 : 0);
