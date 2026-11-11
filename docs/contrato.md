# Contrato de `boxctl` (v1)

`boxctl` es la interfaz canonica de la box de agentes. El MCP, el plugin, las skills y los
scripts del capataz son envoltorios: no tienen logica propia, llaman a `boxctl --json` y deciden
por `ok` y `error.codigo`. Esquema formal: [`contrato.schema.json`](contrato.schema.json).

## Reglas de estabilidad

- `contrato` es un entero. Agregar campos o verbos **no** lo sube; quitar o cambiar el significado
  de un campo, codigo o exit code **si** (y convive un ciclo con el anterior).
- Los codigos de error y sus exit codes no se renumeran: solo se agregan al final.
- Con `--json` hay exactamente un objeto JSON en stdout, tambien ante errores de argumentos o
  excepciones inesperadas. stderr queda para humanos.
- Las operaciones que cambian algo dejan una linea en `/var/log/agent-box.jsonl` del nodo
  (`ts, op, resultado, quien, ...`). Nunca secretos.

## Sobre

```json
{"contrato": 1, "ok": true,  "operacion": "estado", "datos": { ... }}
{"contrato": 1, "ok": false, "operacion": "caja-reiniciar",
 "error": {"codigo": "caja_ocupada", "mensaje": "hay 6 agentes vivos ...", "detalle": {"agentes": 6}}}
```

## Donde corre cada verbo

| Dominio | Verbos | Donde | Por que |
|---|---|---|---|
| Nodo | `estado`, `suites`, `limpiar`, `podar`, `caja-reiniciar`, `mantenimiento`, `logs`, `firewall`, `gc`, `salvar` | En el nodo, como root (`sudo`). Desde la Mac: `--nodo X` los reenvia por `ssh <alias> sudo boxctl --json ...` | Necesitan docker, `/proc` y el workspace del host |
| Pool | `nodos`, `worker lista`, `worker alta`, `worker baja` | En la Mac | `orca` (worktrees, repos, agentes) existe solo ahi |
| Ambos | `version` | Donde sea | |

Excepcion: `limpiar --aplicar` desde la Mac borra los worktrees que Orca conoce con
`orca worktree rm` y el resto con `podar` remoto. En el nodo, `limpiar --aplicar` saltea los de
Orca (`salteados[].motivo = gestionado-por-orca`) para no desincronizarlo.

## Operaciones

| Operacion | Cambia algo | Datos (resumen) | Errores tipicos |
|---|---|---|---|
| `version` | no | `version`, `contrato` | |
| `estado` | no | `alertas[{nivel, que: memoria-caja\|swap-host\|zombies\|disco\|deriva, valor}], nodo, ts, host{carga[3], nproc, totalGb, disponibleGb, swapUsadoGb, discoPct}, caja{estado, runtime, init, memGb, cpus, memUsadaGb, zombies, creada} \| null, capacidad{suitesCaben, workersCaben, agentes, memCajaGb, memLibreGb}, suites{ocupadas}, deriva[{campo, esperado, actual}]` | `config_invalida`, `permiso` |
| `suites` | no | `slots, ocupados[{slot, pid}], libres, agentes` | |
| `limpiar [--aplicar]` | con `--aplicar` | `worktrees[{path, balde, tipo, rama, sucios, sinPush, inactivoDias, orca}], podables[], aplicado, podados[], salteados[]` | |
| `podar <path>` | si | `podado{...}` | `worker_no_existe`, `operacion_invalida` (balde no seguro o fuera del workspace) |
| `caja-reiniciar [--forzar]` | si | `recreada, agentesMatados, deriva[], salvados[]` (corre `salvar --aplicar` antes; si falla, frena salvo `--forzar`) | `caja_ocupada` (hay agentes y no hay `--forzar`) |
| `mantenimiento` | si | `discoPct, podables, cajasHuerfanas[{nombre, hace}]` | |
| `logs [-n N]` | no | `lineas[]` (objetos de la bitacora) | |
| `firewall estado\|aplicar\|confirmar\|quitar\|verificar\|refrescar [--deadman S]` | si (menos estado/verificar) | `accion, salida[]` | `dependencia_faltante`, `error_interno` (verificacion fallida) |
| `gc [--alcance memoria\|procesos\|disco\|todo] [--aplicar]` | con `--aplicar` | `alcance, aplicado, procesos?{memPct, alerta, acciones[{tipo: huerfano\|suite-tiempo\|suite-ram\|memoria-alta, pid, pids[], comm, args, rssMb, edadMin, motivo}]}, tmp?[], caches?[], worktrees?{podables, podados, salteados}` | |
| `salvar [--aplicar]` | con `--aplicar` | `aplicado, ts, salvados[{path, rama, ref, commit?, pusheado, errorPush?}], fallos[], limpios` | |
| `nodos` | no | `nodos[{nombre, habilitado, orcaEnvironment, alcanzable, estado \| error}]` | `config_invalida` (falta nodos.json) |
| `worker lista` | no | `workers[{nodo, id, path, rama, repo, nombre, estado, host, terminales, actividad, seguimiento, agentes[]}]` | `dependencia_faltante` (sin orca) |
| `panel [--servir] [--puerto 4319] [--estado-dir DIR]` | no | `ts, sesiones[] (con terminal), abiertos[] (sin terminal), reloj[{id, fase, etapa, detalle, desde, ultimoCambio, archivos{codigo,tests,e2e,docs}, corrida{tipo, codigo, inicio, ms, gates[{nombre, verde, ms}]} \| null, fallosSeguidos, repo, ultimoGate, esperando[], huecos[], sesion?}], avisos[]`. Cada fila de sesión: `repo, nombre, rama, estado, host, terminales, actividad, seguimiento, caja{wi, fase, etapa} \| null` (se junta con el reloj por el path del worktree). `etapa` = `sin-cambios \| codigo \| tests \| e2e \| docs \| gates \| publicando \| inactiva`; la escribe boxd, no el agente. Además `cajas[]` (el estado completo de boxd por caja, de la Mac y de cada nodo, con `nodo`, `titulo`, `rama`, `eventos[]` y `metricas{viva, memMb, techoMb, cpuPct}`) y `nodos[{nombre, alcanzable, vivas, techosMb, presupuestoMb, error?}]`. Los nodos se leen por ssh en un hilo de fondo (cada 15 s): el panel no espera a ninguno | |
| `worker alta --repo R --rama B [--wi ID] [--base origin/main] [--aplicar]` (`--nodo auto\|X`) | con `--aplicar` | `nodo, environment, repo, rama, base, wi, capacidad, aplicado, worktree?{id, path, rama}` | `capacidad_insuficiente`, `repo_no_registrado`, `nodo_inalcanzable` |
| `worker baja --worktree ID\|PATH --nodo X [--aplicar] [--forzar]` | con `--aplicar` | `nodo, worktree, balde, aplicado` | `worker_no_existe`, `worker_activo`, `worker_sucio` |

## Capacidad (una sola formula)

Implementada en `boxctl.capacidad()` y replicada en `caja/gate-lock.sh`; un test verifica que den
lo mismo. Parametros en `box.env` (`AGENTE_GB`, `SUITE_GB`, `RESERVA_GB`, `SUITES_MIN/MAX`,
`WORKERS_MAX`).

```
suitesCaben  = clamp(floor((MEM_GB - agentes*AGENTE_GB - RESERVA_GB) / SUITE_GB), SUITES_MIN, SUITES_MAX)
workersCaben = max(0, min(WORKERS_MAX - agentes, floor((memLibreGb - SUITE_GB) / AGENTE_GB)))
memLibreGb   = min(limite de la caja - usada por la caja, MemAvailable del host)
```

Ejemplo del informe: 13 GB y 7 agentes -> (13 - 4,2 - 1) / 4 = 1,95 -> 1 suite a la vez.

## Errores y exit codes

| Codigo | Exit | Significa | Que hace el que llama |
|---|---|---|---|
| `error_interno` | 1 | Fallo inesperado (docker, git, orca, timeout) | Reportar `mensaje`; no reintentar en loop |
| `operacion_invalida` | 2 | Argumentos mal, verbo de nodo sin `--nodo` fuera de un nodo, poda no segura | Corregir la llamada |
| `nodo_inalcanzable` | 3 | ssh al nodo fallo (exit 255) | Reintentar una vez (red inestable); despues avisar |
| `capacidad_insuficiente` | 4 | Ningun nodo admite otro worker | No lanzar; esperar |
| `worker_no_existe` | 5 | Worktree/worker desconocido | Revisar `worker lista` |
| `caja_ocupada` | 6 | Hay agentes vivos y la operacion los mataria | Esperar a 0; `--forzar` solo con OK de quien opera |
| `cuota_excedida` | 7 | Reservado: cola de suites vencida (gate-lock sale 75 por compatibilidad) | Reintentar mas tarde |
| `permiso` | 8 | Verbo de nodo sin root | Correr con `sudo` |
| `nodo_desconocido` | 9 | `--nodo` no esta en nodos.json | Revisar nodos.json |
| `dependencia_faltante` | 10 | Falta docker/orca/ssh/agent-box-egress | Instalar (install.sh) |
| `repo_no_registrado` | 11 | El repo no esta en el orcad del nodo | Registrar el repo en Orca para ese environment |
| `config_invalida` | 12 | Falta o esta incompleto box.env o nodos.json | Corregir config |
| `worker_activo` | 13 | El worktree tiene agentes vivos | Esperar o `--forzar` con OK |
| `worker_sucio` | 14 | El worktree tiene trabajo sin commitear o sin pushear | `boxctl salvar --aplicar` y reintentar; `--forzar` con OK |

`gate-lock.sh` (adentro de la caja): exit del comando envuelto; 75 si vence la espera; 2 si el
tope es invalido.

## GC y salvar: reglas

- **Protegidos** (nunca se matan, ni lo que cuelga de ellos): PID 1, `claude`, `codex`, `docker-init`/`tini`,
  `sleep`, `sshd` y el orcad (`.orca-remote/`).
- **Huerfano**: cuelga de PID 1 sin protegido arriba, comm de la lista de candidatos (node, jest, npm,
  mongod, chrome, dotnet, ...), mas viejo que `GC_HUERFANO_MIN`, y su cwd NO esta dentro del worktree de un
  claude vivo (respeta `npm run dev &`). Lo lanzado por `docker exec` (ppid 0) es una sesion viva, no huerfano.
- **Suite**: el proceso de mas arriba que matchea jest/vitest/`dotnet test`/`npm test`. Se corta (con todo
  su subarbol) si pasa `SUITE_MAX_MIN` o si el RSS del subarbol pasa `SUITE_MAX_GB`.
- **Memoria**: al `MEM_ALERTA_PCT` de la caja, alerta (journal, bitacora, `ALERTA_CMD`); al `MEM_CORTE_PCT`,
  corta la suite viva mas grande (una por pasada del vigia, cada 30 s).
- Matar = `kill -TERM` al subarbol, 5 s, `kill -KILL`. Cada kill queda en la bitacora; en dry-run, el plan.
- **salvar**: por cada worktree sucio arma un commit con un index temporal (`read-tree HEAD` + `add -A` +
  `write-tree` + `commit-tree -p HEAD`), sin mover HEAD ni tocar el index o los archivos del agente, lo deja en
  `refs/wip/<rama>/<ts>` y lo pushea a ese mismo ref desde adentro de la caja (ahi estan las credenciales).
  El ref lleva timestamp para no necesitar nunca force. Con la caja caida queda el ref local, que vive en el
  bind-mount y sobrevive al reinicio. Recuperar: `git checkout -b rescate refs/wip/<rama>/<ts>`.
