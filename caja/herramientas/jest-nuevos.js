#!/usr/bin/env node
// Suite de jest que solo falla por tests que la rama rompio.
// Corre la suite entera; si algo falla, vuelve a correr SOLO esos archivos sobre una
// copia de la base (git archive, sin tocar el repo). Un test que tambien falla en la
// base ya estaba roto: se informa y no frena. Uno que en la base pasa, frena.
// Uso (adentro de la caja, parado en el repo): node /opt/caja/jest-nuevos.js origin/main -- --ci --maxWorkers=2
const { execFileSync, spawnSync } = require('child_process');
const fs = require('fs');
const path = require('path');

const base = process.argv[2] || 'origin/main';
const sep = process.argv.indexOf('--');
const extra = sep >= 0 ? process.argv.slice(sep + 1) : [];
const raiz = process.cwd();
fs.mkdirSync(path.join(raiz, '.caja'), { recursive: true });

function correr(dir, args, salida) {
  if (fs.existsSync(salida)) fs.unlinkSync(salida);
  const r = spawnSync('npx', ['jest', ...args, '--json', `--outputFile=${salida}`], { cwd: dir, stdio: ['ignore', 'ignore', 'inherit'] });
  if (!fs.existsSync(salida)) { console.error(`jest no dejo reporte (exit ${r.status})`); process.exit(2); }
  return JSON.parse(fs.readFileSync(salida, 'utf8'));
}

// Por archivo: los nombres de los tests rojos. Un archivo que ni siquiera cargo cuenta entero.
function rojos(reporte, dir) {
  const out = new Map();
  for (const s of reporte.testResults) {
    if (s.status !== 'failed') continue;
    const nombres = s.assertionResults.filter((a) => a.status === 'failed').map((a) => a.fullName);
    out.set(path.relative(dir, s.name), nombres.length ? nombres : ['<el archivo no cargo>']);
  }
  return out;
}

const mio = correr(raiz, extra, path.join(raiz, '.caja', 'test.json'));
const rotos = rojos(mio, raiz);
console.log(`jest: ${mio.numPassedTests} pasaron, ${mio.numFailedTests} fallaron, ${mio.numPendingTests} salteados, ${mio.numTotalTestSuites} archivos`);
if (!rotos.size) process.exit(0);

const copia = '/tmp/caja-base';
fs.rmSync(copia, { recursive: true, force: true });
fs.mkdirSync(copia, { recursive: true });
execFileSync('bash', ['-c', `git archive ${JSON.stringify(base)} | tar -x -C ${copia}`], { cwd: raiz });
fs.symlinkSync(path.join(raiz, 'node_modules'), path.join(copia, 'node_modules'));

const enBase = [...rotos.keys()].filter((f) => fs.existsSync(path.join(copia, f)));
const viejos = enBase.length
  ? rojos(correr(copia, ['--ci', '--maxWorkers=2', '--runTestsByPath', ...enBase], path.join(raiz, '.caja', 'test-base.json')), copia)
  : new Map();

// Lo que falla aca y no en la base se corre de nuevo, solo. Un test que la rama rompio
// falla tambien en soledad; uno que pasa solo es inestable bajo carga (mongod que no
// llega a levantar con la suite entera) y se informa sin frenar.
const sospechosos = [...rotos].filter(([archivo, nombres]) => {
  const yaRotos = new Set(viejos.get(archivo) || []);
  return nombres.some((n) => !yaRotos.has(n));
}).map(([archivo]) => archivo);
const deNuevo = sospechosos.length
  ? rojos(correr(raiz, ['--ci', '--maxWorkers=1', '--runTestsByPath', ...sospechosos], path.join(raiz, '.caja', 'test-reintento.json')), raiz)
  : new Map();

let propios = 0; let previos = 0; let inestables = 0;
for (const [archivo, nombres] of rotos) {
  const yaRotos = new Set(viejos.get(archivo) || []);
  const otraVez = new Set(deNuevo.get(archivo) || []);
  for (const n of nombres) {
    if (yaRotos.has(n)) { previos++; console.log(`ya estaba roto en ${base}: ${archivo} :: ${n}`); }
    else if (otraVez.size === 0) { inestables++; console.log(`inestable (paso al correrlo solo): ${archivo} :: ${n}`); }
    else if (otraVez.has(n) || n.startsWith('<') || [...otraVez][0].startsWith('<')) { propios++; console.log(`ROTO POR ESTA RAMA: ${archivo} :: ${n}`); }
    else { inestables++; console.log(`inestable (paso al correrlo solo): ${archivo} :: ${n}`); }
  }
}
console.log(`jest: ${propios} roto(s) por esta rama; ${previos} ya fallaban en ${base} y ${inestables} inestable(s) (no frenan)`);
process.exit(propios ? 1 : 0);
