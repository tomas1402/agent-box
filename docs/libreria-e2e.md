# Libreria de e2e

Diseño de un catalogo de viajes e2e por repo, que el reloj ([reloj.md](reloj.md)) usa para
decidir que pruebas tiene que correr un work item (WI) antes de poder salir como PR.

**Estado de implementacion**

| Parte | Estado |
|---|---|
| Formato de `e2e/catalogo.json` y de `e2e/propuestas/<wi>.json` | En el codigo (`boxd/internal/reloj/catalogo.go`) |
| Matcheo de criterios contra el catalogo (`Matchear`) y fijado de viajes en el estado (`PinCatalogo`) | En el codigo y con tests, **no expuesto en el CLI**: `caja alta` no lee el catalogo ni los criterios del WI |
| Ejecucion de propuestas y viajes fijados dentro de `publicar` | En el codigo (`aplicarViajes`) |
| Runners permitidos, evidencia fresca y verde, reporte de Playwright y jest | En el codigo |
| Modo `browse` (browser real del operador por MCP) | Diseño. No hay integracion con ninguna herramienta de browser |
| `boxd viaje listar --wi <id>` y la opcion `--catalogo` | No implementado |
| Indice generado `e2e-index.json` con todos los catalogos | No implementado |

## Por que un catalogo

Una herramienta de browser con MCP (un servidor mas una extension del navegador, con las
sesiones y logins del operador) permite que el agente navegue, pero no es un catalogo de
pruebas: no elige viajes segun los criterios de aceptacion, no los versiona y no frena un
`publicar`. Ademas, montar adentro de la caja el perfil de un navegador real es lo contrario
del corte de credenciales del reloj: el agente heredaria cada sesion abierta. Una allowlist de
sitios limita a donde navega la pestaña, no lo que el JavaScript de esa pagina llama despues.

## Dos modos, un catalogo

| Modo | Donde corre | Cuando | Que prueba `publicar` |
|---|---|---|---|
| `headless` | Playwright (u otro runner permitido) en la caja, contra la app local, con fixtures | Siempre. Es el gate desatendido | Exit 0 y el artefacto reescrito en esa corrida |
| `browse` | Browser real del operador; la caja solo habla MCP | El viaje necesita una sesion real | Diseño: video o HAR de esa corrida. Si el browser pide intervencion humana, el WI queda `esperando-persona`: ni verde ni rojo |

En el codigo, `Matchear` fija solo los viajes `headless`; un criterio que solo cubre un viaje
`browse` queda como `esperando`, y uno que nadie cubre queda como hueco.

## Donde viven los archivos

Uno por repo, commiteados:

```
<repo>/e2e/catalogo.json
<repo>/e2e/propuestas/<wi>.json
<repo>/e2e/evidencia/<viaje>/journey.json
```

No hay un repo central ni una base donde se junten. Un WI se prueba contra el catalogo del repo
en el que esta el worktree. Si el recorrido cruza dos apps, el viaje vive en el repo del WI y el
comando pega a la otra app por URL.

## El archivo

`e2e/catalogo.json` tiene una lista `viajes`. Campos que lee el codigo: `id`, `ac` (ids de los
criterios de aceptacion que demuestra), `modo` (`headless` o `browse`), `cmd` (lo que ejecuta
`publicar`), `artefacto` (ruta, relativa al worktree, de la evidencia) y, en las propuestas,
`reemplaza` (id del viaje que sustituye).

```json
{
  "viajes": [
    {
      "id": "usuario-sin-permiso-no-edita",
      "ac": ["AC1"],
      "modo": "headless",
      "cmd": "npx playwright test e2e/usuario-sin-permiso-no-edita.spec.ts",
      "artefacto": "e2e/evidencia/usuario-sin-permiso-no-edita/journey.json"
    }
  ]
}
```

Un viaje de `npx playwright ...` deja su reporte JSON de Playwright en `artefacto` (el reloj
agrega `--reporter=json`; el spec no se toca).

## Que se fija y que se puede agregar

Diseño de `caja alta` (la funcion existe, el CLI aun no la llama):

- Criterio ya cubierto: el `cmd` de ese viaje se copia al estado del host como gate duro
  (`viaje:<id>`). El agente puede editar el spec, pero `publicar` corre la copia: no puede
  cambiarla por `exit 0`.
- Criterio sin viaje: queda un hueco. `publicar` no sale verde hasta que una propuesta lo cubra
  y haya corrido en esa misma pasada (`ac_sin_viaje`).
- Viaje que el WI tiene que cambiar: el viejo se corre igual. La propuesta trae `reemplaza` con
  el id. El viejo puede fallar solo si el nuevo paso y cubre los mismos criterios.

Una propuesta (la escribe el agente en `e2e/propuestas/<wi>.json`) cuenta solo si se cumplen
las cuatro, y el codigo ya lo verifica en `publicar`:

1. El `cmd` arranca con un runner permitido: `npx playwright`, `npx jest`, `npm run e2e`,
   `npm run test:e2e` o `dotnet test`. Comandos con `exit 0` o `touch` se rechazan
   (`viaje_cmd`).
2. Corrio durante este `publicar` y dio cero (`propuesta_roja` si no).
3. El artefacto se reescribio en esa corrida y esta verde: reporte de Playwright sin tests
   fallidos, inestables ni salteados; reporte de jest sin fallidos, pendientes ni `todo`; o un
   journey propio con todos los pasos en `ok: true`. Un `touch` no alcanza.
4. Esta en el catalogo del worktree (`catalogo_sin_propuesta`): lo incorpora el PR, no un
   proceso aparte. El diff del catalogo en el PR es el registro de que cambio.

## Modo browse: diseño pendiente

Un viaje `browse` que se queda esperando intervencion humana no vence solo: `publicar`
respondera `esperando-persona` y no abrira el PR. El codigo ya distingue ese resultado, pero no
hay integracion con ninguna herramienta de browser. La caja seria cliente MCP de un servidor
que corre donde estan los logins, con allowlist del origen de dev de esa app, y sin montar el
perfil ni las cookies del navegador.
