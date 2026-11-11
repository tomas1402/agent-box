# El reloj: una caja por work item

Diseño vigente de `boxd`, el daemon del reloj. Un work item (WI) entra a una caja y
sale como PR solo si un proceso que no es el agente vuelve a correr los gates y todos
dan cero. El agente edita y prueba. No actualiza el WI, no abre el PR y no se sube la RAM.

El mecanismo es el de cualquier cerradura: un archivo de instrucciones largo no se cumple;
se cumple el programa que corre el check y niega la salida.

Se puede ver andando, sin Azure y con docker local, con `ops/demo-caja.sh` (alta, publicar,
baja) y `ops/demo-etapas.sh` (como se deduce la etapa de una caja).

## Piezas

1. **boxd**: un binario por nodo. Dueño de las cajas, de la RAM y de las credenciales de
   escritura (tracker y permiso de abrir PRs). Vive en `boxd/`.
2. **El estado**: un archivo JSON por WI en el host (`--estado-dir`, o `AGENT_BOX_ESTADO`, o
   `~/.local/share/agent-box/wi`; en un nodo, `/var/lib/agent-box/wi`). Lo escribe solo boxd.
   Cada WI tiene un lock (`<id>.lock`, flock): dos `publicar`, o un `latido` en medio de un
   `publicar`, no se pisan el estado.
3. **El panel** (`boxctl panel`): lee ese estado y lo muestra. Si el panel esta caido, el
   reloj sigue frenando la salida.

La herramienta que abre las sesiones de los agentes no se reemplaza: abre la terminal adentro
de la caja y no conoce los gates.

## Verbos

| Verbo | Que hace |
|---|---|
| `caja alta --wi ID --worktree DIR --config harness.json [--titulo T]` | Admite el WI: lee el `harness.config.json` una vez y guarda en el estado los gates, el setup, las reglas, el artefacto e2e y el destino del PR. Con `--docker --aplicar` crea la caja `box-<id>` (etiqueta `agent-box.wi`, sin `--env-file`). |
| `caja baja --wi ID` | Borra la caja. El worktree queda en el host. |
| `preparar --wi ID` | Corre el `setup` del harness adentro de la caja (`npm ci`, etc.). |
| `paso --wi ID --gate NOMBRE` | Corre un gate para ver el avance. No publica y deja el log para el panel. |
| `publicar --wi ID [--descripcion ARCHIVO]` | La unica salida (ver abajo). |
| `estado --wi ID` | El estado guardado de un WI. |
| `lista [--observar] [--metricas]` | Todas las cajas: fase, etapa, RAM y CPU. Es lo que lee el panel. |
| `latido [--wi ID]` | Recalcula la etapa mirando los archivos de la rama. |
| `destrabar --wi ID` | Vuelve a habilitar `publicar` despues de un `escalado`. |
| `reabrir --wi ID` | Saca un WI de `listo`. |
| `comentar --wi ID --informe JSON` | Vuelve a dejar el comentario de un `publicar`. |

Los verbos que crean, borran o publican (`caja alta`, `caja baja`, `preparar`, `publicar`) no
hacen nada sin `--aplicar`: muestran lo que harian. `paso` y los de lectura corren siempre.

## Estados (fase)

| Fase | Quien la pone | Que es verdad |
|---|---|---|
| `admitido` | `caja alta` | Hay caja y worktree. Todavia no se toco ningun archivo. |
| `trabajando` | el primer archivo tocado (lo ve `latido`) | Se esta editando. No dice nada de los gates. |
| `listo` | `publicar` con todos los gates duros en cero | Gates duros verdes en una corrida de boxd, evidencia e2e fresca, comentario escrito en el WI y PR abierto. |
| `frenado` | `publicar` con algun gate o chequeo en rojo | Se anota que fallo (`ultimoGate`) y la cola del log. El agente sigue editando. |
| `escalado` | 3 `publicar` seguidos que fallan en el mismo gate | `publicar` rechaza hasta que una persona corre `destrabar`. La caja no se borra. |

No existe un estado "el agente dice que ya testeo". Un `paso` del agente corre un gate y deja
el log; `publicar` lo ignora y corre todo de nuevo.

## Etapa

Ademas de la fase, cada caja expone en que esta (`latido` y `lista --observar`): `sin-cambios`,
`codigo`, `tests`, `e2e`, `docs`, `gates`, `publicando` o `inactiva` (10 minutos sin tocar un
archivo). La etapa sale de los archivos de la rama y del gate que se esta corriendo, no la
declara el agente. El estado tambien guarda cuantos archivos toco la rama por clase y los
ultimos eventos.

## La unica salida: `publicar`

`boxd publicar --wi <id>` corre en el host, no en la sesion del agente:

1. Frena de entrada si el WI esta `escalado`, ya esta `listo`, otro `publicar` tiene el lock
   (`en_curso`), la sonda ve credenciales o montajes prohibidos en la caja
   (`credencial_en_la_caja`, `caja_sucia`) o el burst no entra en el presupuesto del nodo
   (`presupuesto`).
2. Sube el techo de RAM de esa caja al burst, corre los gates y lo baja al terminar, aunque
   fallen.
3. Corre cada gate `hard: true` del `harness.config.json`, adentro de la caja con `--docker`.
   Cada gate tiene plazo (`timeoutSec`; sin el, 30 minutos). Al vencer se mata el arbol
   entero de procesos y el gate cuenta como fallo (exit 124). Un fallo que ya estaba en la
   base (`baseline`, marcado `preexisting`) no frena.
4. Antes de correr ningun gate ya aplico las reglas del harness (`reglas`), que no gastan RAM
   ni cuentan como fallo: `testsConCodigo` (una rama que toca codigo tiene que tocar tests o
   e2e; si no, `sin_tests`) y `veredicto` (alguien que no es el autor aprobo exactamente este
   commit; si no, `sin_veredicto`, `veredicto_rechazado` o `veredicto_viejo`). La revision se
   anota desde el host con `boxd veredicto --wi ID --aprueba|--rechaza --quien NOMBRE`.
5. Exige evidencia e2e en la ruta que declara `e2e.artifact`: tiene que existir, estar dentro
   del worktree y haberse reescrito durante esta corrida. Si es un reporte conocido, tiene que
   estar verde:
   - reporte JSON de Playwright: al menos un test esperado, y ninguno fallido, inestable
     (`flaky`) ni salteado. Un test salteado no es verde.
   - reporte JSON de jest: al menos un test pasado, y ninguno fallido, pendiente, `todo` ni
     suite fallida.
   - journey propio: `ok` en true y todos los pasos en `ok`.
   Un parrafo en el chat no cuenta.
6. Solo si todo da cero: comentario en el WI y PR contra `prTarget`, con las credenciales del
   host. Nunca mergea ni deploya. El comentario incluye solo las lineas de la salida de los
   gates que empiezan con prefijos conocidos (`jest: `, `lint: `, `ya estaba roto en `,
   `inestable `); el resto de la cola nunca se copia, porque ahi puede aparecer un secreto.
7. Deja el WI `listo`, o `frenado`/`escalado` con el gate que fallo.

### Ensayo y salida real

Por defecto (`--salida registro`) `publicar` es un **ensayo**: corre todo y arma los comandos
de `az`, pero solo los anota en el resultado; no escribe nada en el tracker ni abre el PR, y un
ensayo verde (`ensayo_verde`) no deja el WI en `listo`. Con `--salida ejecutar` corre la CLI
del proveedor usando las credenciales de `--supervisor-env` (por defecto
`/etc/agent-box/supervisor.env`). El proveedor sale de `tracker.provider` en el harness:
`azure` (CLI `az`), `github` (CLI `gh`, con `tracker.repo` como `dueño/repo` y el work item
como issue) o `ninguno` (los gates deciden y no se escribe afuera). Sumar otro es implementar
la interfaz `Proveedor` en `boxd/internal/reloj/proveedor.go`. Sin `--aplicar`, `publicar` solo lista los gates duros que
correria (`seco`).

La salida real nunca se probo contra un Azure real: probala primero con un WI de prueba.

### Viajes e2e en `publicar`

Si el WI tiene viajes fijados (pineados) o propuestas en `e2e/propuestas/<id>.json`,
`publicar` los corre junto con los gates, con evidencia: un viaje de `npx playwright ...` deja
su reporte JSON como artefacto. Una propuesta solo cuenta si su comando arranca con un runner
permitido, corrio en esta pasada y su artefacto es fresco y verde. Un viaje fijado que falla
solo se perdona si una propuesta verde lo reemplaza y cubre los mismos criterios. Ojo: hoy el
CLI no fija viajes (`caja alta` no lee el catalogo); el detalle esta en
[libreria-e2e.md](libreria-e2e.md).

## Quien puede hacer que

| Accion | Agente, adentro de la caja | boxd, en el host |
|---|---|---|
| Editar el worktree | si | no |
| Correr tests | si | si, y solo la corrida de boxd cuenta para salir |
| `git push` de la rama | si, con un token de solo contents | no hace falta |
| Abrir el PR | no hay permiso de PRs | si |
| Escribir en el tracker | no hay token | si |
| Subir `memory.max` | no | si, dentro del presupuesto del nodo |

Las credenciales de escritura viven en `supervisor.env` (modo 600, root), fuera de la caja. boxd
rechaza una caja que monte `docker.sock` o `supervisor.env`, o que traiga en el entorno un token
(`GH_TOKEN`, `GITHUB_TOKEN`, `AZURE_DEVOPS_EXT_PAT`, `AZURE_DEVOPS_PAT`, entre otros): en ese
caso `publicar` no corre ningun gate. Que la imagen de la caja no traiga `gh` logueado ni sudo
es una condicion de diseño que cumple quien arma la imagen; boxd no lo comprueba. Sin ese corte
el reloj seria un cartel.

## RAM

Cada nodo tiene un perfil (opcion `--nodo`): techo de edicion, techo de corrida (burst) y
presupuesto total para cajas. Los valores de partida son hipotesis a medir con el RSS real de
un agente en la caja:

| | Perfil mayor | Perfil menor |
|---|---|---|
| Presupuesto de cajas | 12 GiB | 11 GiB |
| Caja editando: `memory.high` / `memory.max` | 800 MB / 1,2 GB | igual |
| Burst de una corrida, una a la vez | 5 GiB | 5 GiB |

Pasarse de `memory.high` frena el proceso y le hace soltar memoria; pasarse de `max` lo mata.
boxd concede el burst solo si la suma de los `max` de las cajas vivas, con la de este WI al
valor del burst, entra en el presupuesto; si no entra, `publicar` responde `presupuesto` y
no corre nada. El burst se aplica con `docker update` sobre la caja viva (el kernel aplica el
cgroup aunque el runtime sea gVisor); con docker no se puede escribir `memory.high`, asi que el
informe lo marca como no aplicado. No se usa swap como colchon. Las cajas vivas para el
presupuesto se leen de docker.

## Panel

`boxctl panel` (y `boxctl panel --servir`) lee dos fuentes y no escribe nada: la herramienta
que abre las sesiones (repo, rama, estado, terminales, seguimiento) y el directorio de estado
de boxd (fase, etapa, repo, ultimo gate, lo que falta). `boxctl nodos` muestra la capacidad de
cada nodo. Formato en [contrato.md](contrato.md).

## Lo que falta o no esta implementado

- Leer los criterios de aceptacion del WI desde el tracker en `caja alta`, y fijar los viajes
  del catalogo (`PinCatalogo` existe como funcion, no esta expuesta en el CLI).
- Enrutar automaticamente las sesiones que traen un WI hacia su caja (`orca-agent-shell` no lo
  hace: la caja de un WI se agrega a mano como un host SSH mas).
- Imagen de la caja sin `gh` logueado ni sudo, y token de solo contents adentro.
- Jueces LLM como veto adicional. El reloj es el comando con exit code; los jueces podrian
  sumarse despues sin ser la cerradura.
- El proveedor `github` tiene tests de los comandos que arma, pero nunca se corrio contra GitHub.
