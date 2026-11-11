#!/usr/bin/env bash
# Tests livianos, sin Docker ni red: sintaxis bash, shellcheck (si esta) y unittest.
set -uo pipefail
cd "$(dirname "$0")/.." || exit 1
fallos=0
# bash 3.2 (macOS): sin mapfile
SH=()
while IFS= read -r f; do SH+=("$f"); done < <(find . -path ./.git -prune -o \( -name '*.sh' -o -path ./host/orca-agent-shell -o -path ./host/orca-secret-set \) -type f -print | sort)
for f in "${SH[@]}"; do bash -n "$f" || { echo "bash -n FALLA: $f"; fallos=1; }; done
echo "bash -n: ${#SH[@]} archivos"
if command -v shellcheck >/dev/null 2>&1; then
  shellcheck -S warning "${SH[@]}" && echo "shellcheck: ok" || fallos=1
else
  echo "shellcheck: no instalado (brew install shellcheck), salteado"
fi
python3 -m py_compile boxctl/boxctl plugin/agent-box/mcp/boxctl_mcp.py && echo "py_compile: ok" || fallos=1
for j in .claude-plugin/marketplace.json plugin/agent-box/.claude-plugin/plugin.json plugin/agent-box/.mcp.json config/nodos.json.example docs/contrato.schema.json; do
  python3 -m json.tool "$j" >/dev/null || { echo "JSON invalido: $j"; fallos=1; }
done
python3 -m unittest discover -s tests -p 'test_*.py' || fallos=1
exit $fallos
