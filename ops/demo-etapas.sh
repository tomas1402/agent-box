#!/bin/bash
set -e
REPO="$(cd "$(dirname "$0")/.." && pwd)"; D="${1:-$HOME/.cache/agent-box-demo}/etapas"
rm -rf "$D"; mkdir -p "$D/wt" "$D/estado"
(cd "$D/wt" && git init -q -b feature/us-1 . && git -c user.name=t -c user.email=t@t commit -q --allow-empty -m base)
cat > "$D/harness.json" <<'J'
{"gates":[{"name":"lint","cmd":"sleep 1; echo ok","hard":true},{"name":"e2e","cmd":"sleep 2; mkdir -p e2e && echo '{}' > e2e/report.json","hard":true}],
 "e2e":{"artifact":"e2e/report.json"},"prTarget":"qa","branchPrefix":"feature/us-","prTitlePrefix":"[t] ","tracker":{"provider":"azure","repo":"demo"}}
J
(cd "$REPO/boxd" && go build -o "$D/boxd" ./cmd/boxd)
b() { "$D/boxd" --json --estado-dir "$D/estado" "$@"; }
ver() { python3 -c 'import json,sys; c=json.load(sys.stdin)["datos"]["cajas"][0]; a=c.get("actividad") or {}; print(c["fase"], "|", a.get("etapa"), "|", a.get("detalle"), "|", a.get("archivos"))'; }
echo "Como boxd deduce en que esta una caja. Cada linea: fase | etapa | archivo o gate actual | archivos tocados por clase"
b --aplicar caja alta --wi 1 --worktree "$D/wt" --config "$D/harness.json" >/dev/null
echo "1 alta       : $(b lista --observar | ver)"
mkdir -p "$D/wt/src" "$D/wt/e2e"
echo a > "$D/wt/src/servicio.ts";              echo "2 codigo     : $(b lista --observar | ver)"
sleep 1; echo a > "$D/wt/src/servicio.spec.ts"; echo "3 tests      : $(b lista --observar | ver)"
sleep 1; echo a > "$D/wt/e2e/alta.spec.ts";     echo "4 e2e        : $(b lista --observar | ver)"
b --aplicar publicar --wi 1 > "$D/pub.json" &
sleep 0.6; echo "5 en publicar: $(b lista | ver)"
sleep 1.3; echo "6 en publicar: $(b lista | ver)"
echo "  (un segundo publicar mientras corre el primero tiene que responder en_curso)"
echo "  segundo publicar a la vez: $(b --aplicar publicar --wi 1 | python3 -c 'import json,sys; print(json.load(sys.stdin)["error"]["codigo"])')"
wait
python3 - "$D" <<'P'
import json,sys; D=sys.argv[1]
d=json.load(open(D+"/pub.json")); i=d["datos"]["informe"]
print("7 publicar   : ok=%s fase=%s publico=%s" % (d["ok"], i["fase"], i["publico"]))
print("  gates      :", [(g["nombre"], g["exit"], str(g["ms"])+" ms") for g in i["gates"]])
w=json.load(open(D+"/estado/1.json"))
print("  historia   :", " > ".join(e["que"]+"="+e["valor"] for e in w["eventos"]))
P
