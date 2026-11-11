---
name: usar-caja
description: Usar cuando hay que resolver un work item (bug, historia, ticket) con agent-box, o cuando piden "trabajalo en una caja", "abrí una caja para el 123", "mandalo por el reloj", "publicá el PR". Lleva el trabajo por una caja de boxd de punta a punta: alta, preparación, e2e primero, implementación, revisión por otro agente, ensayo y publicación. El PR lo abre boxd, nunca el agente.
---

# Trabajar un work item en una caja

Vos orquestás. No abrís el PR, no comentás el ticket y no decidís que los tests pasaron: eso lo
hace `boxd` corriendo los gates desde afuera. Tu trabajo es que lleguen en verde.

`boxd` lee sus opciones de `~/.config/agent-box/boxd.conf` (imagen, runtime, docker), o del
archivo que diga la variable `AGENT_BOX_CONF` si te pasaron una. Si
`boxd lista` falla porque no encuentra `boxd`, pedile a la persona que corra `make install` en el
repo de agent-box. `boxd ayuda` lista todos los verbos.

## Antes de empezar

Necesitás tres cosas. Si falta alguna, preguntá; no la inventes.

1. El número del work item y su título.
2. El worktree: un checkout del repo en la rama de trabajo.
3. El harness de ese repo: el JSON con sus gates. No lo escribas vos ni lo edites para que algo pase.

## Recorrido

1. **Alta.** `boxd --aplicar caja alta --wi ID --worktree DIR --config HARNESS --titulo "TITULO"`
2. **Preparar.** `boxd --aplicar preparar --wi ID` instala dependencias adentro de la caja.
3. **Entender.** Leé el ticket y sus criterios de aceptación. Decí en una línea qué va a cambiar
   para quien usa el sistema. Si un criterio admite dos lecturas, preguntá antes de escribir código.
4. **El e2e primero.** Escribí (o delegá) la prueba de punta a punta que demuestra el pedido, y
   corré su gate con `boxd paso --wi ID --gate NOMBRE`. Tiene que fallar (`ROJO` en la línea del
   gate y `paso: gate_rojo`), y por el motivo correcto: leé el final de la salida que muestra
   boxd. Un e2e que nunca falló no prueba nada.
5. **Implementar.** Un solo escritor por worktree. Si delegás, el encargo dice: el objetivo, los
   archivos que puede tocar, lo que no puede hacer (push, PR, tracker, editar el harness) y la
   evidencia que tiene que devolver. Los tests se corren con `boxd paso`, que los corre adentro de
   la caja; no en la máquina. `paso` ve el worktree como está, con o sin commit. Cuando los gates
   pasan, commiteá: la revisión y la publicación son sobre un commit.
6. **Revisar.** Leé el diff vos, no el resumen de quien lo escribió. Si el harness exige veredicto,
   lo da alguien que no sea el autor (otro agente, de ser posible otro modelo, o una persona):
   `boxd veredicto --wi ID --aprueba --quien NOMBRE`, o `--rechaza --nota "motivo"`. En NOMBRE va
   quién revisó de verdad (una persona, o por ejemplo `agente-revisor`); nunca el autor. Vale para
   ese commit: un commit nuevo pide revisión nueva.
7. **Ensayo.** `boxd --aplicar publicar --wi ID` corre todos los gates y no escribe nada. Repetí
   hasta `ensayo_verde`.
8. **Publicar.** Solo con el visto bueno de la persona para pushear la rama. Después:
   `boxd --aplicar --salida ejecutar publicar --wi ID --descripcion ARCHIVO`. `ejecutar` vale para
   cualquier proveedor. La descripción es opcional pero conviene: un Markdown, guardado fuera del
   repo, con una sección `## Qué cambia` y otra `## A tener en cuenta al revisar`; boxd las copia
   al comentario del ticket junto con los e2e y los gates.
9. **Cerrar.** Pasale a la persona el link del PR que imprime boxd y `boxd --aplicar caja baja --wi ID`.
   Si el harness no tiene tracker (`"provider": "ninguno"`), boxd deja el work item `listo` sin
   abrir PR: decíselo así, sin inventar un link.

## Cuando algo frena

| boxd dice | Qué hacer |
|---|---|
| `gates_rojos` | Leé el final de la salida del gate que muestra boxd, arreglá, y de nuevo |
| `sin_tests` | La rama cambia código y no toca tests: escribí el test, no desactives la regla |
| `sin_veredicto`, `veredicto_viejo` | Falta la revisión de este commit (paso 6) |
| `e2e_ausente`, `e2e_viejo`, `e2e_rojo` | El gate de e2e no dejó su reporte en esta corrida, o tiene tests rojos o salteados |
| `credencial_en_la_caja` | La caja tiene un token en su entorno. No lo saques vos: avisá |
| `presupuesto`, `en_curso` | Hay otra corrida usando la RAM. Esperá y reintentá |
| `escalado` | Tres fallos seguidos en el mismo gate. Pará y contale a la persona qué probaste; solo ella corre `boxd destrabar` |

## Lo que no se hace

- Editar el harness, saltear o marcar `skip` un test, o bajar una regla para que algo pase.
- Pushear, abrir el PR o escribir en el tracker por fuera de `boxd publicar`.
- Decir "listo" sin haber visto `ensayo_verde` o `publicado` en la salida de boxd.
- Meter credenciales en la caja.
