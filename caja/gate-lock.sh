#!/usr/bin/env bash
# gate-lock v2: cola de suites de test con slots por RAM. Corre ADENTRO de la caja (y en la Mac).
#
#   gate-lock.sh <grupo> <tope|auto> -- <comando...>
#   gate-lock.sh test 2 -- npm test -- --ci --maxWorkers=2      (firma de v1: sigue valiendo)
#   gate-lock.sh test auto -- npx jest --maxWorkers=2
#
# Slots (se recalculan en cada vuelta de la espera):
#   techo   = min(tope, clamp(floor((MEM - agentes*AGENTE_GB - RESERVA_GB) / SUITE_GB), SUITES_MIN, SUITES_MAX))
#   por RAM = ocupados + floor((MemAvailable - RESERVA_GB) / SUITE_GB)      (RAM libre REAL, no una constante)
#   slots   = clamp(por RAM, 1, techo)          -> la primera suite siempre entra (sin inanicion)
# MEM y MemAvailable salen de `free -m` (bajo gVisor = la caja); agentes = procesos claude/codex
# (pgrep por nombre exacto: gate-lock es bash, no se cuenta a si mismo). Limites: /etc/agent-box/limites.env,
# que escribe orca-agent-shell desde box.env; sin el archivo, los defaults de abajo.
# El techo es la misma formula que boxctl.capacidad(); tests/test_gate_lock.py lo verifica.
#
# Exclusion: con `flock` cada slot es un lock de archivo que
# el kernel suelta solo si el proceso muere (sin slots huerfanos). Sin flock (macOS) cae a mkdir +
# pid con recuperacion por rename atomico.
# El comando corre con oom_score_adj=1000 (choom, si existe): si algo tiene que morir, que sea la suite.
# Bajo gVisor el efecto no esta confirmado; el corte real lo hace `boxctl gc --alcance memoria`.
#
# Exit: el del comando; 75 si vence GATE_LOCK_TIMEOUT (default 1800 s); 2 si el tope es invalido.
set -uo pipefail

GRUPO="${1:?grupo}"; TOPE="${2:?tope o auto}"; shift 2; [ "${1:-}" = "--" ] && shift
[ "$#" -gt 0 ] || { echo "gate-lock: falta el comando" >&2; exit 2; }
case "$TOPE" in auto) ;; ''|*[!0-9]*) echo "gate-lock: tope invalido: $TOPE (numero o auto)" >&2; exit 2 ;; esac
BASE="/tmp/harness-gate-lock/$GRUPO"; mkdir -p "$BASE" 2>/dev/null; chmod 1777 "$BASE" 2>/dev/null || true
TIMEOUT="${GATE_LOCK_TIMEOUT:-1800}"; t0=$(date +%s)

AGENTE_GB=0.6; SUITE_GB=4; RESERVA_GB=1; SUITES_MIN=1; SUITES_MAX=3
LIMITES=/etc/agent-box/limites.env
# shellcheck disable=SC1090
[ -r "$LIMITES" ] && . "$LIMITES"

decimas() { awk -v x="$1" 'BEGIN { printf "%d", x * 10 + 0.5 }'; }

# imprime "techo disponible_en_decimas_de_GB" (-1 si no hay free)
medir() {
  local mem_mb disp_mb agentes num s
  if ! command -v free >/dev/null 2>&1; then echo "$SUITES_MAX -1"; return; fi
  read -r mem_mb disp_mb < <(free -m | awk '/^Mem:/{print $2, $7}')
  agentes=$(pgrep -c -x 'claude|codex' 2>/dev/null || true); agentes=${agentes:-0}
  num=$(( mem_mb * 10 / 1024 - agentes * $(decimas "$AGENTE_GB") - $(decimas "$RESERVA_GB") ))
  if [ "$num" -lt 0 ]; then s=0; else s=$(( num / $(decimas "$SUITE_GB") )); fi
  [ "$s" -lt "$SUITES_MIN" ] && s=$SUITES_MIN
  [ "$s" -gt "$SUITES_MAX" ] && s=$SUITES_MAX
  echo "$s $(( ${disp_mb:-0} * 10 / 1024 ))"
}

# GATE_LOCK_SIN_FLOCK=1 fuerza el modo sin flock aunque exista (para probar los dos modos en Linux).
TIENE_FLOCK=0; [ -z "${GATE_LOCK_SIN_FLOCK:-}" ] && command -v flock >/dev/null 2>&1 && TIENE_FLOCK=1
alive() { kill -0 "$1" 2>/dev/null; }

ocupado() {  # ocupado <i>: exit 0 si el slot i esta tomado
  local d="$BASE/slot-$1"
  if [ "$TIENE_FLOCK" = 1 ]; then
    [ -e "$d.lock" ] || return 1
    if ( flock -n 9 ) 9>>"$d.lock"; then return 1; else return 0; fi
  fi
  local p; p=$(cat "$d/pid" 2>/dev/null || echo "")
  [ -n "$p" ] && alive "$p"
}

slots() {
  local techo disp ocupados=0 i libres s
  read -r techo disp < <(medir)
  if [ "$TOPE" != auto ] && [ "$TOPE" -lt "$techo" ]; then techo=$TOPE; fi
  if [ "$disp" -lt 0 ]; then echo "$techo"; return; fi     # sin free (Mac): solo techo
  for i in $(seq 1 "$techo"); do ocupado "$i" && ocupados=$((ocupados+1)); done
  libres=$(( (disp - $(decimas "$RESERVA_GB")) / $(decimas "$SUITE_GB") )); [ "$libres" -lt 0 ] && libres=0
  s=$(( ocupados + libres )); [ "$s" -lt 1 ] && s=1; [ "$s" -gt "$techo" ] && s=$techo
  echo "$s"
}

# Slot huerfano (modo mkdir): rename atomico y despues borrar; si dos lo ven muerto, solo uno gana el mv.
recuperar() {
  local d="$1" p
  p=$(cat "$d/pid" 2>/dev/null || echo "")
  [ -n "$p" ] || return 0
  alive "$p" && return 0
  mv "$d" "$d.huerfano.$$" 2>/dev/null && rm -r -f -- "$d.huerfano.$$"
  return 0
}

correr() {
  if command -v choom >/dev/null 2>&1; then choom -n 1000 -- "$@"; else "$@"; fi
}

mine=""; S=1
while :; do
  S=$(slots)
  for i in $(seq 1 "$S"); do
    d="$BASE/slot-$i"
    if [ "$TIENE_FLOCK" = 1 ]; then
      exec 9>>"$d.lock"
      if flock -n 9; then mine="$i"; break; fi
      exec 9>&-
    else
      if mkdir "$d" 2>/dev/null; then echo $$ > "$d/pid"; mine="$i"; break; fi
      recuperar "$d"
    fi
  done
  [ -n "$mine" ] && break
  if [ $(( $(date +%s) - t0 )) -ge "$TIMEOUT" ]; then
    echo "gate-lock: timeout esperando slot de '$GRUPO' ($S slots por RAM)" >&2; exit 75
  fi
  sleep "${GATE_LOCK_POLL:-5}"
done
echo "gate-lock: slot-$mine/$S de $GRUPO tomado (espera $(( $(date +%s) - t0 )) s)" >&2
if [ "$TIENE_FLOCK" = 0 ]; then trap 'rm -r -f -- "$BASE/slot-$mine"' EXIT INT TERM; fi
correr "$@"; rc=$?
exit $rc
