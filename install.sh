#!/usr/bin/env bash
# Instalador idempotente de un nodo de la box de agentes. Corre EN el nodo, como root.
#
#   sudo ./install.sh --node <nombre>                  (= --check) solo informa, no escribe nada
#   sudo ./install.sh --node <nombre> --aplicar        instala/actualiza lo que difiere, con backup
#   sudo ./install.sh --node <nombre> --aplicar --recrear-caja   ademas recrea la caja (solo sin agentes)
#
# <nombre> es cualquier nombre valido ([a-z0-9-]+) con un config/box.env.<nombre>.example en el repo
# (ejemplos incluidos: nodo-a, nodo-b).
#
# Copiar el repo al nodo desde la Mac (sin git en el nodo):
#   rsync -a --delete ./ nodo-a-admin:/tmp/agent-box/ && ssh nodo-a-admin sudo /tmp/agent-box/install.sh --node nodo-a
#
# Que hace con --aplicar (cada paso se saltea si ya esta igual):
#   1. /etc/agent-box/box.env: lo crea desde config/box.env.<nodo>.example si NO existe. Si existe,
#      nunca lo pisa (es del operador): muestra el diff contra el ejemplo.
#   2. Binarios y scripts (boxctl, orca-agent-shell, orca-secret-set, agent-box-egress, unidades
#      systemd, allowlist): backup del anterior en /root/backups-agent-box/<ts>/ y reemplazo.
#   3. Red docker RED con subred y nombre de bridge fijos (para el firewall).
#   4. ORCA_STATE (~/.orca persistente de la caja), dueño agent.
#   5. Archivos para adentro de la caja en $WORKSPACE/.agent-box/caja (gate-lock y los hooks);
#      se activan con caja/instalar-en-caja.sh, paso aparte (ver docs/despliegue.md).
#   6. Timers: agent-box-mantenimiento (04:00) y agent-box-egress-refresh, GC y vigia.
#   7. --recrear-caja: boxctl caja-reiniciar (rechaza con agentes vivos: exit 6).
# NO hace: aplicar el firewall (paso propio con auto-rollback), tocar secretos, buildear la imagen,
# tocar red del host, INPUT/OUTPUT de iptables, ni otras aplicaciones del host.
set -euo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
NODO=""; MODO=check; RECREAR=0
while [ $# -gt 0 ]; do
  case "$1" in
    --node) NODO="${2:?nombre del nodo}"; shift 2 ;;
    --check|--dry-run) MODO=check; shift ;;
    --aplicar) MODO=aplicar; shift ;;
    --recrear-caja) RECREAR=1; shift ;;
    -h|--help) sed -n '2,26p' "$0"; exit 0 ;;
    *) echo "argumento desconocido: $1" >&2; exit 2 ;;
  esac
done
[[ "$NODO" =~ ^[a-z0-9-]+$ ]] || { echo "falta --node <nombre> (solo [a-z0-9-])" >&2; exit 2; }
[ "$(id -u)" = 0 ] || { echo "correr con sudo" >&2; exit 1; }

TS=$(date +%Y%m%d-%H%M%S)
BK=/root/backups-agent-box/$TS
CONF=/etc/agent-box/box.env
EJEMPLO="$REPO/config/box.env.$NODO.example"
[ -f "$EJEMPLO" ] || { echo "no existe $EJEMPLO (copiar config/box.env.nodo-a.example con ese nombre)" >&2; exit 2; }
cambios=0; avisos=0

paso() { printf '\n== %s\n' "$*"; }
info() { printf '  %s\n' "$*"; }
aviso() { printf '  AVISO: %s\n' "$*"; avisos=$((avisos+1)); }
hacer() {  # hacer "<descripcion>" cmd...
  local d="$1"; shift
  if [ "$MODO" = aplicar ]; then "$@"; info "hecho: $d"; else info "haria: $d"; fi
  cambios=$((cambios+1))
}
respaldar() { [ -e "$1" ] || return 0; mkdir -p "$BK$(dirname "$1")"; cp -a "$1" "$BK$1"; }

paso "0. Prerrequisitos ($NODO, modo $MODO)"
grep -q 'VERSION_ID="24.04"' /etc/os-release 2>/dev/null || aviso "no es Ubuntu 24.04"
for b in docker python3 jq curl ipset iptables getent sha256sum systemd-run rsync; do
  command -v "$b" >/dev/null 2>&1 && info "ok $b" || aviso "falta $b (apt install)"
done
id agent >/dev/null 2>&1 && info "ok usuario agent (uid $(id -u agent))" || aviso "falta el usuario agent"

paso "1. box.env (unica fuente de limites)"
if [ -f "$CONF" ]; then
  info "existe $CONF: no se pisa"
  diff -u "$EJEMPLO" "$CONF" | sed 's/^/    /' || true
else
  hacer "crear $CONF desde $(basename "$EJEMPLO") (revisar MEM_GB antes de recrear la caja)" \
    install -D -m 644 "$EJEMPLO" "$CONF"
fi
if [ -f "$CONF" ]; then
  # shellcheck disable=SC1090
  . "$CONF"
else
  # shellcheck disable=SC1090
  . "$EJEMPLO"
fi
if [ "$RUNTIME" = runsc ]; then
  docker info --format '{{json .Runtimes}}' 2>/dev/null | grep -q runsc && info "ok runtime runsc registrado en docker" \
    || aviso "runsc no registrado en docker (instalar gVisor: runsc install && systemctl restart docker)"
fi
[ -e /etc/orca-agent/box.env ] && info "box.env viejo (/etc/orca-agent/box.env) queda sin uso; borrarlo despues de validar"

paso "2. Binarios, scripts y unidades"
instalar() {  # instalar <origen-en-repo> <destino> <modo>
  local src="$REPO/$1" dst="$2" modo="$3"
  if [ -f "$dst" ] && cmp -s "$src" "$dst"; then info "igual  $dst"; return 0; fi
  [ -f "$dst" ] && info "difiere $dst" || info "ausente $dst"
  if [ "$MODO" = aplicar ]; then respaldar "$dst"; fi
  hacer "instalar $dst" install -D -m "$modo" "$src" "$dst"
}
instalar boxctl/boxctl                      /usr/local/bin/boxctl 755
instalar host/orca-agent-shell              /usr/local/bin/orca-agent-shell 755
instalar host/orca-secret-set               /usr/local/bin/orca-secret-set 755
instalar host/firewall/agent-box-egress.sh  /usr/local/sbin/agent-box-egress 750
instalar config/egress-allowlist.txt        /etc/agent-box/egress-allowlist.txt 644
for u in "$REPO"/host/systemd/*; do instalar "host/systemd/$(basename "$u")" "/etc/systemd/system/$(basename "$u")" 644; done
touch_log() { touch /var/log/agent-box.jsonl; chmod 640 /var/log/agent-box.jsonl; }
[ -f /var/log/agent-box.jsonl ] || hacer "crear /var/log/agent-box.jsonl" touch_log

paso "3. Red docker de la caja ($RED, $RED_SUBNET, bridge $RED_BRIDGE)"
if docker network inspect "$RED" >/dev/null 2>&1; then
  sub=$(docker network inspect -f '{{range .IPAM.Config}}{{.Subnet}}{{end}}' "$RED")
  [ "$sub" = "$RED_SUBNET" ] && info "ok red $RED" || aviso "red $RED existe con subred $sub (box.env dice $RED_SUBNET)"
else
  hacer "crear red $RED" docker network create --driver bridge --subnet "$RED_SUBNET" \
    -o "com.docker.network.bridge.name=$RED_BRIDGE" "$RED"
fi

paso "4. ~/.orca persistente ($ORCA_STATE)"
if [ -d "$ORCA_STATE" ]; then info "ok $ORCA_STATE"; else
  hacer "crear $ORCA_STATE (agent, 700)" install -d -m 700 -o agent -g agent "$ORCA_STATE"
fi

if [ -n "${CLAUDE_STATE:-}" ]; then
  if [ -d "$CLAUDE_STATE" ]; then info "ok $CLAUDE_STATE (~/.claude persistente)"; else
    hacer "crear $CLAUDE_STATE (agent, 700; se siembra desde la caja viva)" \
      install -d -m 700 -o agent -g agent "$CLAUDE_STATE"
  fi
fi

paso "5. Archivos para adentro de la caja ($WORKSPACE/.agent-box/caja)"
DST="$WORKSPACE/.agent-box/caja"
if [ -d "$DST" ] && diff -rq "$REPO/caja" "$DST" >/dev/null 2>&1; then info "igual $DST"; else
  copiar_caja() { install -d -o agent -g agent "$WORKSPACE/.agent-box"; rm -rf "$DST"; cp -a "$REPO/caja" "$DST"; chown -R agent:agent "$DST"; }  # sin rsync: no hace falta en el nodo
  hacer "copiar caja/ a $DST" copiar_caja
fi

paso "6. Timers"
# Los del GC arrancan en dry-run (GC_APLICAR vacio en box.env): solo leen ps y escriben la bitacora.
TIMERS="agent-box-mantenimiento.timer agent-box-egress-refresh.timer agent-box-gc.timer agent-box-gc-disco.timer agent-box-vigia.timer"
recargar() { systemctl daemon-reload; # shellcheck disable=SC2086
  systemctl enable --now $TIMERS; }
todos=1; for t in $TIMERS; do systemctl is-enabled "$t" >/dev/null 2>&1 || todos=0; done
if [ "$todos" = 1 ]; then
  info "ok timers habilitados"; [ "$MODO" = aplicar ] && systemctl daemon-reload
else hacer "habilitar timers agent-box-* (GC en modo $( grep -q '^GC_APLICAR=--aplicar' "$CONF" 2>/dev/null && echo APLICA || echo dry-run))" recargar; fi

paso "7. Caja"
if [ "$RECREAR" = 1 ]; then
  if [ "$MODO" = aplicar ]; then
    /usr/local/bin/boxctl caja-reiniciar || { echo "  caja-reiniciar rechazado o fallido (ver arriba); nada mas se toco" >&2; exit 6; }
  else info "haria: boxctl caja-reiniciar (rechaza si hay agentes vivos)"; fi
else
  info "no se recrea (pasar --recrear-caja); la deriva se ve en: sudo boxctl estado"
fi
if [ -x /usr/local/bin/boxctl ] && [ -f "$CONF" ]; then
  /usr/local/bin/boxctl --json estado 2>/dev/null | python3 -c '
import json,sys
d=json.load(sys.stdin)
if not d.get("ok"): print("  estado:", d.get("error")); sys.exit()
for x in d["datos"]["deriva"]: print("  deriva:", x["campo"], "esperado", x["esperado"], "actual", x["actual"])
print("  capacidad:", d["datos"]["capacidad"])' || true
fi

paso "Resumen"
info "modo $MODO: $cambios cambio(s) $( [ "$MODO" = check ] && echo pendientes || echo aplicados ), $avisos aviso(s)"
[ "$MODO" = aplicar ] && [ -d "$BK" ] && info "backup: $BK (rollback: copiar de vuelta + systemctl daemon-reload)"
exit 0
