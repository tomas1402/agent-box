#!/bin/bash
# Demo guiada: una caja real en Docker, de punta a punta, en un minuto.
# No toca ningun repo tuyo ni escribe en ningun tracker: usa un directorio temporal
# y el modo ensayo de boxd. Al final borra las cajas que creo.
#   bash ops/demo-caja.sh [directorio-de-trabajo]
set -euo pipefail
REPO="$(cd "$(dirname "$0")/.." && pwd)"
D="${1:-$HOME/.cache/agent-box-demo}/caja"
IMG="${IMAGEN:-debian:bookworm-slim}"

command -v docker >/dev/null || { echo "Falta docker. Instalalo y volve a correr."; exit 1; }
docker info >/dev/null 2>&1 || { echo "Docker no responde. Abrilo (Docker Desktop o el servicio) y volve a correr."; exit 1; }
command -v go >/dev/null || { echo "Falta Go 1.22 o mas nuevo para compilar boxd."; exit 1; }
command -v python3 >/dev/null || { echo "Falta python3."; exit 1; }

limpiar() { docker rm -f box-9001 box-9002 >/dev/null 2>&1 || true; }
trap limpiar EXIT
limpiar
rm -rf "$D"; mkdir -p "$D/wt" "$D/wt2" "$D/estado"

# El harness dice que gates corre el reloj. Aca hay uno solo, que ademas deja la evidencia de e2e.
cat > "$D/harness.json" <<'J'
{"gates":[{"name":"dentro","cmd":"echo host=$(hostname) techo=$(cat /sys/fs/cgroup/memory.max); mkdir -p e2e && echo '{}' > e2e/report.json","hard":true}],
 "e2e":{"artifact":"e2e/report.json"},"prTarget":"main","branchPrefix":"feature/","prTitlePrefix":"[demo] ",
 "tracker":{"provider":"azure","repo":"demo","orgUrl":"https://dev.azure.com/ejemplo","project":"Demo"}}
J
(cd "$REPO/boxd" && go build -o "$D/boxd" ./cmd/boxd)
b() { "$D/boxd" --json --estado-dir "$D/estado" --docker --imagen "$IMG" --runtime "${RUNTIME:-runc}" "$@"; }
techo() { echo "$(( $(docker inspect -f '{{.HostConfig.Memory}}' "$1") / 1048576 )) MB"; }
campo() { python3 -c "import json,sys; d=json.load(sys.stdin); print(eval(sys.argv[1]))" "$1"; }

echo "1. Se admite el work item 9001 y se crea su caja (imagen $IMG)."
b --aplicar caja alta --wi 9001 --worktree "$D/wt" --config "$D/harness.json" --titulo "Work item de demostracion" | campo "'   caja creada: %s' % d['datos']['creada']"
echo "   techo de RAM mientras se edita: $(techo box-9001)"
echo "   variables con credenciales adentro de la caja: $(docker exec box-9001 env | grep -cE '_TOKEN=|_PAT=|SECRET' || true)"

echo "2. publicar corre los gates ADENTRO de la caja, con la RAM subida para la corrida."
b --aplicar publicar --wi 9001 > "$D/pub.json" || true
python3 - "$D" <<'P'
import json,sys; d=json.load(open(sys.argv[1]+"/pub.json")); i=d["datos"]["informe"]
print("   el gate vio:", i["gates"][0]["cola"].strip())
print("   RAM subio: %s, y volvio a bajar: %s" % (i["memoria"]["subio"], i["memoria"]["bajo"]))
print("   resultado:", i.get("codigo") or "publicado", "(ensayo: los gates dieron cero y no se escribio en ningun lado)")
print("   lo que habria ejecutado con --salida az:", " ".join(d["datos"]["argv"][1][:4]), "...")
P
echo "   techo de RAM despues de la corrida: $(techo box-9001)"

echo "3. Una caja con un token en su entorno no publica: el reloj la revisa antes de correr nada."
docker run -d --name box-9002 --label agent-box.wi=9002 -e GH_TOKEN=falso -v "$D/wt2:$D/wt2" --entrypoint sleep "$IMG" infinity >/dev/null
"$D/boxd" --json --estado-dir "$D/estado" --aplicar caja alta --wi 9002 --worktree "$D/wt2" --config "$D/harness.json" >/dev/null
(b --aplicar publicar --wi 9002 || true) | campo "'   resultado: %s, gates corridos: %d' % (d['error']['codigo'], len(d['datos']['informe']['gates'] or []))"

echo "4. Se borran las cajas."
b --aplicar caja baja --wi 9001 | campo "'   %s' % d['datos']"
limpiar
echo "   cajas de la demo que quedan: $(docker ps -aq --filter name=box-900 | wc -l | tr -d ' ')"
echo
echo "Listo. Para verlas en el tablero mientras corren: make panel"
