---
name: operar-box
description: Usar cuando haya que operar la box de agentes (nodos como nodo-a y nodo-b) — ver si un nodo esta sano, cuanta capacidad queda, elegir nodo para un worker, ver la cola de suites, liberar disco, reiniciar la caja o mirar la auditoria. Todo pasa por boxctl.
---

# Operar la box de agentes

Todo pasa por `boxctl` (contrato en `docs/contrato.md` del repo agent-box). Desde la maquina del operador, los
verbos de nodo llevan `--nodo <nombre>` y se reenvian por ssh. Usar siempre `--json` y decidir por
`ok` y `error.codigo`, nunca por el texto.

| Quiero | Comando |
|---|---|
| Qué se está trabajando, en qué repo, seguimiento | `boxctl panel --servir` (http://127.0.0.1:4319) o `boxctl --json panel` |
| Salud y capacidad de todo el pool | `boxctl --json nodos` |
| Un nodo (deriva de config incluida) | `boxctl --json --nodo nodo-a estado` |
| Cola de suites | `boxctl --json --nodo nodo-a suites` |
| Dónde lanzar un worker | `boxctl --json worker alta --repo <r> --rama <b>` (plan) y despues `--aplicar` |
| Podar worktrees | `boxctl --json --nodo nodo-a limpiar` (dry-run) → leer baldes → `limpiar --aplicar` |
| Reiniciar la caja | `boxctl --json --nodo nodo-a caja-reiniciar` (si hay agentes: `caja_ocupada`, exit 6) |
| Auditoria | `boxctl --json --nodo nodo-a logs -n 50` |

## Reglas
- `caja_ocupada`: NO reintentar con `--forzar` sin OK explicito de quien opera: mata a los agentes vivos.
- `capacidad_insuficiente`: no lanzar; informar `capacidad` de cada nodo y esperar.
- `deriva` no vacia en `estado`: la caja no coincide con box.env. Se corrige con `caja-reiniciar`
  cuando no haya agentes; avisar a quien opera en una linea.
- Firewall (`firewall aplicar`): solo con OK de quien opera; queda con auto-rollback hasta `firewall confirmar`,
  que va DESPUES de `firewall verificar` en verde.
- Nunca pegar secretos. Los secretos van por `orca-secret-set` en el host, valor por stdin.
