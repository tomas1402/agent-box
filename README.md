<p align="center">
  <img src="docs/assets/logo.svg" width="96" alt="agent-box">
</p>

<h1 align="center">agent-box</h1>

<p align="center">
  Una caja por tarea para tus agentes de código, y un reloj que no los deja decir "listo" sin pruebas.
</p>

> **Estado: alfa.** Su autor lo usa a diario para abrir PRs reales, pero la interfaz todavía
> cambia. Comandos, mensajes y documentación están en español.

## Qué problema resuelve

Si corrés agentes de código (Claude Code, Codex y similares) en serio, te encontrás con tres cosas:

- **El agente dice que terminó y no terminó.** Corrió los tests "más o menos", salteó el que
  fallaba o abrió el PR con el lint roto.
- **Varios agentes a la vez tumban la máquina.** Cada uno lanza su suite de tests y la RAM no alcanza.
- **El agente tiene tus credenciales.** Puede abrir el PR, comentar el ticket o pushear a donde quiera.

agent-box separa esas tres cosas del agente:

| | El agente, adentro de su caja | `boxd`, afuera |
|---|---|---|
| Editar código y correr tests | sí | no edita |
| Decidir que los gates pasaron | no | sí: los corre de nuevo él |
| Abrir el PR y comentar el ticket | no tiene credenciales | sí, y solo con todo en verde |
| Subirse la RAM | no | sí, una suite por vez |

## Cómo funciona

```
 vos / un orquestador                       boxd (el reloj, en el host)
        │                                            │
        │  boxd caja alta --wi 123                   │  crea box-123 (Docker), sin credenciales
        ▼                                            │
 ┌─ box-123 ───────────────┐   boxd publicar         │  sube la RAM de la caja
 │ el agente edita y prueba │ ◄────────────────────── │  corre los gates ADENTRO: lint, tests, build, e2e
 │ su worktree, nada más    │                         │  exige la evidencia de e2e de esa corrida
 └──────────────────────────┘                         │  baja la RAM
                                                      ▼
                                   todo en cero → comenta el ticket y abre el PR
                                   algo en rojo  → "frenado"; a los 3 fallos, "escalado"
```

- **Una caja por work item.** Un contenedor con el worktree montado y un techo de memoria bajo
  mientras se edita. Con [gVisor](https://gvisor.dev) si el host lo tiene.
- **Los gates salen de un archivo, no del agente.** `boxd` los copia al admitir el work item; el
  agente puede cambiar el repo, pero no lo que se va a correr para dejarlo salir.
- **El e2e tiene que dejar evidencia.** Un reporte de Playwright o de jest escrito en esa corrida.
  Un test salteado o inestable no cuenta como verde.
- **Cada caja dice en qué está.** Código, tests, e2e, gates o PR. `boxd` lo deduce de los archivos
  que tocó la rama; no se lo pregunta al agente.

## Empezar en 5 minutos

Necesitás Docker andando, Go 1.22 o más nuevo, Python 3.9 o más nuevo y `make`.

```bash
git clone https://github.com/tomas1402/agent-box.git
cd agent-box
make demo
```

`make demo` tarda unos 10 segundos: compila `boxd`, crea una caja de verdad, corre un gate adentro y
la borra. No toca ningún repo tuyo ni ningún servicio externo; solo escribe en
`~/.cache/agent-box-demo`. Deberías ver algo así:

```
1. Se admite el work item 9001 y se crea su caja (imagen debian:bookworm-slim).
   caja creada: True
   techo de RAM mientras se edita: 1200 MB
   variables con credenciales adentro de la caja: 0
2. publicar corre los gates ADENTRO de la caja, con la RAM subida para la corrida.
   el gate vio: host=8d98e4180a9b techo=5368709120
   RAM subio: True, y volvio a bajar: True
   resultado: ensayo_verde (ensayo: los gates dieron cero y no se escribio en ningun lado)
3. Una caja con un token en su entorno no publica: el reloj la revisa antes de correr nada.
   resultado: credencial_en_la_caja, gates corridos: 0
4. Se borran las cajas.
```

Después:

```bash
make panel        # el tablero en http://127.0.0.1:4319
make demo-etapas  # sin Docker: cómo boxd deduce la etapa de cada caja
make test         # todos los tests
make ayuda        # el resto
```

## Palabras que usa este README

| Palabra | Qué es |
|---|---|
| Work item | El ticket (bug, historia) que se está resolviendo. Su número identifica a la caja: `box-123` |
| Worktree | El checkout del repo, en la rama de trabajo, que se monta en la caja |
| Gate | Un comando que tiene que salir con cero para que el cambio pueda salir: lint, tests, build, e2e |
| Harness | El archivo JSON con los gates de un repo |
| Reloj | `boxd`: el programa que corre los gates desde afuera de la caja y decide la salida |
| Ensayo | Un `publicar` que corre todo y no escribe nada. Es el modo por defecto |
| Etapa | En qué está la caja ahora: código, tests, e2e, gates o PR |
| Nodo | Una máquina que corre cajas |

## Tu primer work item

Necesitás un repo git con una rama de trabajo ya creada, y una imagen de Docker que tenga lo que
tus gates usan (para un proyecto Node, `node:22-bookworm`).

**1. Escribí el harness de tu repo.** Hay uno de ejemplo en
[`examples/harness.node.json`](examples/harness.node.json):

```json
{
  "setup": ["npm ci"],
  "gates": [
    {"name": "lint",  "cmd": "npm run lint",  "hard": true},
    {"name": "test",  "cmd": "npm test",      "hard": true, "timeoutSec": 1200},
    {"name": "build", "cmd": "npm run build", "hard": true},
    {"name": "e2e",   "cmd": "mkdir -p e2e && PLAYWRIGHT_JSON_OUTPUT_FILE=e2e/report.json npx playwright test --reporter=json", "hard": true}
  ],
  "e2e": {"artifact": "e2e/report.json"},
  "prTarget": "main",
  "branchPrefix": "feature/",
  "prTitlePrefix": "",
  "reglas": {"testsConCodigo": true, "veredicto": true},
  "tracker": {"provider": "github", "repo": "mi-org/mi-repo"}
}
```

- Guardalo fuera del repo que se está cambiando, para que el agente no lo pueda editar.
- `boxd` lo copia al admitir el work item. Si lo cambiás después, volvé a correr `caja alta`.
- `e2e.artifact` es la evidencia: el gate de e2e tiene que escribir ese archivo en cada corrida.
  Queda sin commitear en el worktree; agregalo a tu `.gitignore`.

**2. Admití el work item y creá su caja.**

```bash
make build
caja() { ./bin/boxd --docker --imagen node:22-bookworm --runtime runc "$@"; }

caja --aplicar caja alta --wi 123 --worktree /ruta/al/worktree --config harness.json --titulo "Lo que pide el ticket"
caja --aplicar preparar --wi 123      # corre el setup del harness adentro de la caja
```

- `--runtime runc` es el runtime común de Docker. Si tu host tiene gVisor, usá `--runtime runsc`.
- El estado de cada work item se guarda en `~/.local/share/agent-box/wi`. Para usar otro lugar,
  `--estado-dir DIR` o la variable `AGENT_BOX_ESTADO`.
- Con `make install`, `boxd` queda en `~/.local/bin` y no hace falta el `./bin/`.

**3. Trabajá.** Vos o tu agente editan el worktree. Para ver cómo viene un gate sin publicar:

```bash
caja paso --wi 123 --gate test
caja estado --wi 123
caja lista --observar --metricas
```

**4. Publicá.** Primero el ensayo, que corre todos los gates y no escribe nada:

```bash
caja --aplicar publicar --wi 123
```

```
  ok    lint                   exit 0        8 s
  ok    test                   exit 0      276 s
  ok    build                  exit 0       14 s
  ok    e2e                    exit 0       16 s

ensayo_verde: los gates dieron cero. No se escribió nada; con --salida ejecutar se comenta el work item y se abre el PR.
```

Si un gate falla, `boxd` muestra el final de su salida y el work item queda `frenado`. A los tres
fallos seguidos en el mismo gate queda `escalado` y hace falta `boxd destrabar`.

Para la publicación de verdad necesitás la CLI de tu proveedor con sesión iniciada (`gh` para
GitHub, `az` para Azure DevOps) y la rama ya pusheada:

```bash
caja --aplicar --salida ejecutar publicar --wi 123 --descripcion pr.md
```

`boxd` vuelve a correr todos los gates y, solo si dan cero, comenta el work item y abre el PR.
Nunca mergea. Al terminar, `caja --aplicar caja baja --wi 123` borra la caja; el worktree queda.

`./bin/boxd ayuda` lista todos los verbos. Los que crean, borran o publican no hacen nada sin
`--aplicar`: muestran lo que harían. Con `--json` cualquier verbo devuelve el objeto completo.

## Lo que el reloj fuerza

Los gates son la mitad. La otra mitad son reglas de proceso que se prenden en el harness
(`"reglas"`) y que el agente no puede saltear, porque las chequea `boxd` antes de correr nada:

| Regla | Qué exige | Si no se cumple |
|---|---|---|
| `testsConCodigo` | Una rama que cambia código cambia también tests o e2e | `sin_tests` |
| `veredicto` | Alguien que no es el autor aprobó exactamente este commit | `sin_veredicto`, `veredicto_viejo` |
| (siempre) | El e2e dejó su reporte en esta corrida, sin tests rojos ni salteados | `e2e_ausente`, `e2e_viejo`, `e2e_rojo` |
| (siempre) | La caja no tiene credenciales | `credencial_en_la_caja` |
| (siempre) | Tres fallos seguidos en el mismo gate frenan hasta que una persona destraba | `escalado` |

La revisión la anota quien revisa, desde afuera de la caja, y vale para un commit:

```bash
boxd veredicto --wi 123 --aprueba --quien "ana"
boxd veredicto --wi 123 --rechaza --quien "ana" --nota "falta el caso de lista vacía"
```

## Trackers

`tracker.provider` en el harness elige la integración. `boxd` no guarda credenciales: usa la CLI
del proveedor, que es la que tiene tu sesión.

| `provider` | CLI | Work item | Notas |
|---|---|---|---|
| `github` | `gh` | Un issue de `tracker.repo` (`dueño/repo`) | El PR referencia el issue |
| `azure` | `az` | Un work item de Azure Boards | `orgUrl` y `project` opcionales; el PR queda linkeado |
| `ninguno` | — | — | Los gates deciden; no se comenta ni se abre nada |

Sumar otro (GitLab, Jira) es implementar la interfaz `Proveedor` de
[`boxd/internal/reloj/proveedor.go`](boxd/internal/reloj/proveedor.go): tres funciones que arman comandos.

## Usarlo con un agente

El repo trae un plugin de Claude Code con la skill `usar-caja`: le enseña al agente el recorrido
completo (alta, e2e primero, implementación, revisión, ensayo, publicación) y qué hacer con cada
respuesta de `boxd`.

```
/plugin marketplace add /ruta/a/agent-box
/plugin install agent-box@agent-box
```

Dejá tus opciones fijas en `~/.config/agent-box/boxd.conf`, una por línea, para que el agente no
tenga que pasarlas:

```
docker=true
imagen=node:22-bookworm
runtime=runc
herramientas=/ruta/a/agent-box/caja/herramientas
```

Después alcanza con pedirle: "trabajá el 123 en una caja; el worktree está en X y el harness en Y".

## El tablero

`make panel` abre un tablero local con una tarjeta por caja: en qué etapa está, qué archivo o
gate tiene entre manos, RAM y CPU, los gates de la última corrida con sus tiempos y las que están
frenadas. Lee el estado que escribe `boxd`; no escribe nada. Escucha solo en `127.0.0.1`. Si tenés
Orca instalado, lista también tus sesiones.

## Gates que no castigan la deuda vieja

En un repo real, el lint y los tests ya vienen con cosas rotas. Dos herramientas que se montan en
la caja (`--herramientas caja/herramientas`) resuelven eso sin aflojar el control:

- [`lint-diff.js`](caja/herramientas/lint-diff.js): falla solo por errores en líneas que la rama agregó o cambió.
- [`jest-nuevos.js`](caja/herramientas/jest-nuevos.js): si un test falla, lo corre también sobre la
  rama base. Lo que ya fallaba ahí se informa; lo que rompió la rama, frena.

## Varias máquinas

`boxctl` opera uno o más hosts ("nodos") por ssh: salud, capacidad, limpieza de procesos huérfanos,
firewall de salida con lista de dominios permitidos y respaldo del trabajo sin commitear antes de
reiniciar. Su contrato JSON está en [`docs/contrato.md`](docs/contrato.md). Esta parte nació para
la infraestructura del autor y es la menos generalizada del repo: ver los límites, abajo.

## Límites conocidos

- **Trackers: GitHub y Azure DevOps.** El de GitHub tiene tests de los comandos que arma, pero
  todavía no abrió un PR real. GitLab y Jira no están.
- **Idioma: español** en comandos, JSON y mensajes.
- **`boxd` no reparte credenciales.** Deja la caja sin tokens y usa los del host; darle al agente un
  token acotado para `git push` es tarea tuya.
- **El agente no se lanza solo.** agent-box crea la caja y controla la salida; cómo metés al agente
  adentro (ssh, `docker exec`, tu orquestador) queda de tu lado.
- **`boxctl` y `host/` asumen Linux y systemd**, y usan Orca (el gestor de worktrees y sesiones de
  agentes que usa el autor) para listar sesiones. El tablero anda sin Orca: esa sección queda vacía.
- **Sin gVisor igual funciona**, con `--runtime runc`, pero el aislamiento es el de Docker.

## Qué hay en el repo

| Ruta | Qué es |
|---|---|
| `boxd/` | El reloj (Go): cajas, gates, estado, publicación |
| `boxctl/boxctl` | CLI para operar nodos y servir el tablero (Python, un archivo, sin dependencias) |
| `panel/` | El tablero (HTML, sin dependencias) |
| `caja/` | Lo que corre adentro de una caja: herramientas de gates y cola de suites |
| `host/` | Lo que corre en un nodo: firewall de salida, timers, shell de entrada |
| `image/` | Dockerfile de una caja completa (Node, .NET, Playwright, MongoDB) |
| `examples/` | Harness de ejemplo |
| `plugin/` | Plugin de Claude Code: la skill `usar-caja` y un servidor MCP de `boxctl` |
| `docs/` | Contrato de `boxctl`, diseño del reloj y de la librería de e2e |
| `tests/` | Tests de `boxctl` y de los scripts; los de `boxd` están junto al código |

## Tests

```bash
make test
```

No necesitan Docker ni red. Lo que sí necesita Docker es `make demo`.

## Seguridad

agent-box reduce lo que un agente puede romper, pero no es una frontera de seguridad certificada.
Leé [`SECURITY.md`](SECURITY.md) antes de darle a una caja acceso a algo que te importe, y para
reportar un problema.

## Contribuir

Ver [`CONTRIBUTING.md`](CONTRIBUTING.md).

## Licencia

[MIT](LICENSE).
