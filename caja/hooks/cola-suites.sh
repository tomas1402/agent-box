#!/usr/bin/env bash
# PreToolUse (matcher Bash) — cola obligatoria de suites de test dentro de la caja.
#
# Una suite que no pasa por gate-lock no espera turno: con N agentes, N suites con 11 workers
# de jest cada una saturan la caja (carga alta y memoria al 87%). Este hook
# rechaza (exit 2) cualquier runner de tests que no vaya envuelto en
#   ~/.claude/harness/gate-lock.sh test <tope|auto> -- <comando>
# y le devuelve al agente el comando exacto para reintentar.
#
# Igual que guard-destructivo.sh: no hay variable para apagarlo. Lo entre comillas no cuenta
# (echo "npm test" o un grep no son una suite).
# Limites conocidos (es una guarda cooperativa, no una frontera de seguridad):
#   - cubre solo Claude Code; codex no ejecuta hooks.
#   - lo entrecomillado no se mira, asi que bash -c 'npx jest' pasa.
# El conteo de agentes de gate-lock/boxctl usa el nombre de proceso 'claude' (comm=claude).
# Cuenta tambien sesiones auxiliares, o sea que peca de conservador..
set -uo pipefail

# shellcheck disable=SC2088  # se imprime literal para que el agente lo copie
LOCK='~/.claude/harness/gate-lock.sh'
INPUT=$(cat)
CMD=$(printf '%s' "$INPUT" | jq -r '.tool_input.command // empty' 2>/dev/null)
[ -z "$CMD" ] && exit 0

# Sin lo entrecomillado, en una linea, minusculas.
SIN_COMILLAS=$(printf '%s' "$CMD" | tr '\n' ' ' | sed -E "s/'[^']*'//g; s/\"[^\"]*\"//g" | tr '[:upper:]' '[:lower:]' | tr -s ' ')

# Posicion de comando: inicio, despues de ; & | ( && || o de "-- " (lo que corre un envoltorio),
# con env VAR=x opcionales.
PRE='(^|[;&|(]|-- ) *([a-z_][a-z0-9_]*=[^ ]* +)*'
RUNNER="${PRE}((npm|pnpm|yarn) +(test|t|run +test[a-z0-9:_-]*)( |$)|(npx +|pnpm +exec +|yarn +)?(jest|vitest)( |$)|dotnet +test( |$))"

printf '%s' "$SIN_COMILLAS" | grep -qE "$RUNNER" || exit 0

# Ya va por la cola (grupo test): pasa. Se exige el grupo "test" para que no se esquive con otro.
printf '%s' "$SIN_COMILLAS" | grep -qE 'gate-lock\.sh +test +(auto|[0-9]+) +-- ' && exit 0

# Consultas que no corren tests.
printf '%s' "$SIN_COMILLAS" | grep -qE -- '--(listtests|version|help|showconfig)( |$)' && exit 0

sugerido=""
compuesto=0
printf '%s' "$CMD" | grep -qE '(&&|\|\||;|\|)' && compuesto=1
if [ "$compuesto" = 0 ]; then
  c="$CMD"
  if printf '%s' "$SIN_COMILLAS" | grep -qE '(jest|^ *(npm|pnpm|yarn) )' && ! printf '%s' "$SIN_COMILLAS" | grep -qE 'vitest|dotnet' \
     && ! printf '%s' "$SIN_COMILLAS" | grep -qE -- '--maxworkers|--runinband|(^| )-i( |$)'; then
    # npm/pnpm/yarn test pasan flags al script despues de "--"; jest directo, no.
    if printf '%s' "$SIN_COMILLAS" | grep -qE '^ *(npm|pnpm|yarn) ' && ! printf '%s' "$c" | grep -qE ' -- '; then
      c="$c -- --maxWorkers=2"
    else
      c="$c --maxWorkers=2"
    fi
  fi
  # gate-lock ejecuta "$@": un VAR=x al principio necesita env adelante.
  printf '%s' "$c" | grep -qE '^ *[A-Za-z_][A-Za-z0-9_]*=' && c="env $c"
  sugerido="$LOCK test auto -- $c"
fi

{
  printf 'BLOQUEADO por cola-suites.sh (cola obligatoria de suites de la caja)\n\n'
  printf 'Las suites de test esperan turno: los slots se calculan por la RAM libre de la caja.\n'
  if [ -n "$sugerido" ]; then
    printf 'Reintenta exactamente asi:\n\n  %s\n' "$sugerido"
  else
    printf 'El comando es compuesto: envolve SOLO la parte de tests, por ejemplo:\n\n  cd <dir> && %s test auto -- npx jest --maxWorkers=2\n' "$LOCK"
  fi
  printf '\nNo busques una variante que esquive la cola.\n'
} >&2
exit 2
