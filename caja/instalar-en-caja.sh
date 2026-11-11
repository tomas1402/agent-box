#!/usr/bin/env bash
# Activa gate-lock v2 y el hook de cola de suites ADENTRO de la caja (usuario agent, ~/.claude).
# Idempotente. Los archivos los deja install.sh en $WORKSPACE/.agent-box/caja (bind-mount).
#
#   ssh nodo-a 'bash -s' < caja/instalar-en-caja.sh
#
# Afecta a las sesiones de Claude Code que arranquen DESPUES (los hooks se leen al iniciar).
# Rollback: cp ~/.claude/harness/gate-lock.sh.bak-agent-box ~/.claude/harness/gate-lock.sh y
#           cp ~/.claude/settings.json.bak-agent-box ~/.claude/settings.json
# Ojo: si algun script de sincronizacion copia ~/.claude/harness/gate-lock.sh desde otra maquina
# hacia la caja, pisaria esta version con una vieja: mantener ambas copias al dia.
set -euo pipefail
SRC="${AGENT_BOX_CAJA:-/home/agent/workspace/.agent-box/caja}"
[ -f "$SRC/gate-lock.sh" ] || { echo "falta $SRC/gate-lock.sh (correr install.sh en el host)" >&2; exit 1; }
mkdir -p ~/.claude/harness ~/.claude/hooks
[ -f ~/.claude/harness/gate-lock.sh ] && [ ! -f ~/.claude/harness/gate-lock.sh.bak-agent-box ] && \
  cp -a ~/.claude/harness/gate-lock.sh ~/.claude/harness/gate-lock.sh.bak-agent-box
install -m 755 "$SRC/gate-lock.sh" ~/.claude/harness/gate-lock.sh
install -m 755 "$SRC/hooks/cola-suites.sh" ~/.claude/hooks/cola-suites.sh

python3 - <<'PY'
import json, os, shutil
p = os.path.expanduser("~/.claude/settings.json")
s = json.load(open(p)) if os.path.exists(p) else {}
if os.path.exists(p) and not os.path.exists(p + ".bak-agent-box"):
    shutil.copy2(p, p + ".bak-agent-box")
pre = s.setdefault("hooks", {}).setdefault("PreToolUse", [])
pre[:] = [m for m in pre if not any("cola-suites.sh" in h.get("command", "") for h in m.get("hooks", []))]
pre.append({"matcher": "Bash", "hooks": [{"type": "command", "command": 'bash "$HOME/.claude/hooks/cola-suites.sh"'}]})
json.dump(s, open(p, "w"), indent=2)
print("settings.json: hook cola-suites registrado")
PY

# Verificacion: el hook corta una suite suelta y deja pasar la encolada.
printf %s '{"tool_input":{"command":"npx jest"}}' | bash ~/.claude/hooks/cola-suites.sh >/dev/null 2>&1 && echo "FALLA: no bloquea npx jest" || echo "ok bloquea npx jest"
printf %s '{"tool_input":{"command":"~/.claude/harness/gate-lock.sh test auto -- npx jest --maxWorkers=2"}}' | bash ~/.claude/hooks/cola-suites.sh && echo "ok deja pasar la encolada"
[ -r /etc/agent-box/limites.env ] && echo "ok limites.env: $(tr '\n' ' ' < /etc/agent-box/limites.env)" || echo "AVISO: sin /etc/agent-box/limites.env (caja creada por el wrapper viejo): gate-lock usa defaults"
