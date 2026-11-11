#!/usr/bin/env bash
# agent-box-egress — allowlist de salida para la caja de agentes, aplicada EN EL HOST.
#
# Patron del devcontainer oficial de Claude Code (.devcontainer/init-firewall.sh: ipset hash:net
# + rangos de api.github.com/meta + dominios resueltos + verificacion example.com/api.github.com),
# con dos diferencias deliberadas:
#   1. Se aplica en la cadena DOCKER-USER del host, solo para el trafico que ENTRA desde el bridge
#      de la caja (RED_BRIDGE). No toca INPUT ni OUTPUT del host: el ssh de admin, el wifi y las otras
#      aplicaciones y VPNs del host quedan igual. Por eso no puede dejar a nadie afuera del nodo.
#   2. Corta la LAN, CGNAT/Tailscale (100.64/10) y link-local/metadata antes de la allowlist.
#
#   agent-box-egress aplicar [--deadman SEG]   arma ipset + cadena; programa auto-rollback
#   agent-box-egress confirmar                 cancela el auto-rollback (despues de verificar)
#   agent-box-egress verificar                 pruebas desde ADENTRO de la caja
#   agent-box-egress refrescar                 recarga las IPs (timer cada 30 min; ipset swap atomico)
#   agent-box-egress quitar                    saca el salto y la cadena (rollback total)
#   agent-box-egress estado                    muestra reglas y tamaño del set
#
# Fuente de config: /etc/agent-box/box.env (RED_BRIDGE, DNS, LAN_BLOQUEADA) y
# /etc/agent-box/egress-allowlist.txt (un dominio por linea).
set -euo pipefail
IFS=$'\n\t'

CONF=/etc/agent-box/box.env
ALLOW=/etc/agent-box/egress-allowlist.txt
CADENA=AGENT-BOX-EGRESS
SET=agent-box-egress
BOX=orca-agent-box

[ "$(id -u)" = 0 ] || { echo "correr como root" >&2; exit 1; }
# shellcheck disable=SC1090
. "$CONF"
: "${RED_BRIDGE:?box.env sin RED_BRIDGE}"
LAN_BLOQUEADA="${LAN_BLOQUEADA:-10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,100.64.0.0/10,169.254.0.0/16}"
DNS="${DNS:-1.1.1.1,9.9.9.9}"

es_cidr() { [[ "$1" =~ ^[0-9]{1,3}(\.[0-9]{1,3}){3}(/[0-9]{1,2})?$ ]]; }

llenar_set() {
  local destino="$1" gh cidr dom ip n=0
  ipset create "$destino" hash:net -exist
  ipset flush "$destino"
  gh=$(curl -fsS --max-time 20 https://api.github.com/meta) || { echo "ERROR: no se pudo leer api.github.com/meta" >&2; return 1; }
  echo "$gh" | jq -e '.web and .api and .git' >/dev/null || { echo "ERROR: meta de GitHub incompleta" >&2; return 1; }
  while read -r cidr; do
    es_cidr "$cidr" || continue            # descarta IPv6 (la red de la caja es solo IPv4)
    ipset add "$destino" "$cidr" -exist; n=$((n+1))
  done < <(echo "$gh" | jq -r '(.web + .api + .git + .packages)[]')
  while read -r dom; do
    dom="${dom%%#*}"; dom="${dom// /}"; [ -n "$dom" ] || continue
    # getent sale 2 si el dominio no resuelve; con pipefail eso abortaba el script
    # entero y la caja quedaba sin firewall al boot.
    ips=$(getent ahostsv4 "$dom" 2>/dev/null | awk '{print $1}' | sort -u || true)
    [ -n "$ips" ] || { echo "AVISO: no resuelve $dom" >&2; continue; }
    while read -r ip; do es_cidr "$ip" && ipset add "$destino" "$ip" -exist && n=$((n+1)); done <<<"$ips"
  done < "$ALLOW"
  echo "set $destino: $n entradas"
}

armar_cadena() {
  local d red
  iptables -N "$CADENA" 2>/dev/null || true
  iptables -F "$CADENA"
  iptables -A "$CADENA" -m conntrack --ctstate ESTABLISHED,RELATED -j RETURN
  IFS=',' read -ra _dns <<<"$DNS"
  for d in "${_dns[@]}"; do
    iptables -A "$CADENA" -d "$d" -p udp --dport 53 -j RETURN
    iptables -A "$CADENA" -d "$d" -p tcp --dport 53 -j RETURN
  done
  IFS=',' read -ra _lan <<<"$LAN_BLOQUEADA"
  for red in "${_lan[@]}"; do iptables -A "$CADENA" -d "$red" -j REJECT --reject-with icmp-admin-prohibited; done
  iptables -A "$CADENA" -m set --match-set "$SET" dst -j RETURN
  iptables -A "$CADENA" -j REJECT --reject-with icmp-admin-prohibited
  # Salto unico desde DOCKER-USER, solo para lo que entra por el bridge de la caja.
  iptables -C DOCKER-USER -i "$RED_BRIDGE" -j "$CADENA" 2>/dev/null || iptables -I DOCKER-USER 1 -i "$RED_BRIDGE" -j "$CADENA"
}

quitar() {
  while iptables -C DOCKER-USER -i "$RED_BRIDGE" -j "$CADENA" 2>/dev/null; do iptables -D DOCKER-USER -i "$RED_BRIDGE" -j "$CADENA"; done
  iptables -F "$CADENA" 2>/dev/null || true
  iptables -X "$CADENA" 2>/dev/null || true
  echo "egress: reglas quitadas (la caja vuelve a salida libre)"
}

en_caja() { docker exec "$BOX" bash -c "$1" >/dev/null 2>&1; }

verificar() {
  local fallos=0
  if en_caja 'curl -fsS --max-time 5 https://example.com'; then echo "FALLA: example.com responde"; fallos=1; else echo "ok example.com bloqueado"; fi
  if en_caja 'curl -fsS --max-time 8 https://api.github.com/zen'; then echo "ok api.github.com"; else echo "FALLA: api.github.com"; fallos=1; fi
  if en_caja 'curl -sS --max-time 8 -o /dev/null https://api.anthropic.com'; then echo "ok api.anthropic.com"; else echo "FALLA: api.anthropic.com"; fallos=1; fi
  if en_caja 'curl -sS --max-time 8 -o /dev/null https://dev.azure.com'; then echo "ok dev.azure.com"; else echo "FALLA: dev.azure.com"; fallos=1; fi
  if en_caja 'curl -sS --max-time 8 -o /dev/null https://registry.npmjs.org'; then echo "ok registry.npmjs.org"; else echo "FALLA: registry.npmjs.org"; fallos=1; fi
  local gw; gw=$(ip route | awk '/default/{print $3; exit}')
  if en_caja "timeout 4 bash -c '</dev/tcp/$gw/80'"; then echo "FALLA: LAN alcanzable ($gw:80)"; fallos=1; else echo "ok LAN cortada ($gw)"; fi
  if en_caja 'getent hosts github.com'; then echo "ok DNS desde la caja"; else echo "FALLA: DNS desde la caja"; fallos=1; fi
  return $fallos
}

case "${1:-}" in
  aplicar)
    deadman=600; [ "${2:-}" = --deadman ] && deadman="${3:?segundos}"
    llenar_set "$SET"
    armar_cadena
    # Auto-rollback: si nadie confirma en $deadman s, se quitan las reglas solas.
    systemctl stop agent-box-egress-deadman.timer 2>/dev/null || true
    systemd-run --unit agent-box-egress-deadman --on-active="${deadman}s" /usr/local/sbin/agent-box-egress quitar >/dev/null
    echo "egress aplicado; auto-rollback en ${deadman}s salvo 'agent-box-egress confirmar'"
    ;;
  confirmar)
    systemctl stop agent-box-egress-deadman.timer 2>/dev/null || true
    echo "egress confirmado (sin auto-rollback)"
    ;;
  refrescar)
    iptables -nL "$CADENA" >/dev/null 2>&1 || { echo "egress no aplicado: nada que refrescar"; exit 0; }
    llenar_set "$SET-nuevo"
    ipset swap "$SET-nuevo" "$SET"
    ipset destroy "$SET-nuevo"
    echo "egress refrescado"
    ;;
  verificar) verificar ;;
  quitar) quitar ;;
  estado)
    iptables -nvL "$CADENA" 2>/dev/null || echo "cadena $CADENA ausente"
    ipset list "$SET" -terse 2>/dev/null || echo "set $SET ausente"
    systemctl list-timers agent-box-egress-deadman.timer --no-pager 2>/dev/null | sed -n 2p || true
    ;;
  *) sed -n '2,21p' "$0"; exit 2 ;;
esac
