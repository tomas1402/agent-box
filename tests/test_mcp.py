import json
import os
import subprocess
import tempfile
import unittest
from pathlib import Path

from _comun import RAIZ

SRV = str(RAIZ / "plugin" / "agent-box" / "mcp" / "boxctl_mcp.py")

FAKE = """#!/usr/bin/env python3
import json, sys
args = sys.argv[1:]
if "caja-rota" in args:
    print(json.dumps({"contrato": 1, "ok": False, "error": {"codigo": "nodo_inalcanzable", "mensaje": "x"}})); sys.exit(3)
print(json.dumps({"contrato": 1, "ok": True, "datos": {"argv": args}}))
"""


class Mcp(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.mkdtemp()
        self.fake = Path(self.dir, "boxctl")
        self.fake.write_text(FAKE); self.fake.chmod(0o755)

    def sesion(self, *msgs):
        entrada = "\n".join(json.dumps(m) for m in msgs) + "\n"
        r = subprocess.run(["python3", SRV], input=entrada, capture_output=True, text=True, timeout=20,
                           env={**os.environ, "BOXCTL": str(self.fake)})
        return [json.loads(l) for l in r.stdout.splitlines()]

    def test_handshake_y_lista(self):
        rs = self.sesion({"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": {"protocolVersion": "2025-06-18"}},
                         {"jsonrpc": "2.0", "method": "notifications/initialized"},
                         {"jsonrpc": "2.0", "id": 2, "method": "tools/list"})
        self.assertEqual(len(rs), 2)
        self.assertEqual(rs[0]["result"]["serverInfo"]["name"], "agent-box")
        nombres = {t["name"] for t in rs[1]["result"]["tools"]}
        self.assertIn("box_estado", nombres)
        self.assertFalse(any("reiniciar" in n or "firewall" in n for n in nombres))

    def test_call_arma_el_argv_de_boxctl(self):
        rs = self.sesion({"jsonrpc": "2.0", "id": 3, "method": "tools/call",
                          "params": {"name": "box_estado", "arguments": {"nodo": "nodo-a"}}})
        r = rs[0]["result"]
        self.assertFalse(r["isError"])
        self.assertEqual(r["structuredContent"]["datos"]["argv"], ["--json", "--nodo", "nodo-a", "estado"])

    def test_alta_sin_aplicar_no_pasa_aplicar(self):
        rs = self.sesion({"jsonrpc": "2.0", "id": 4, "method": "tools/call",
                          "params": {"name": "box_worker_alta", "arguments": {"repo": "r", "rama": "b"}}})
        self.assertNotIn("--aplicar", rs[0]["result"]["structuredContent"]["datos"]["argv"])

    def test_error_del_contrato_es_iserror(self):
        rs = self.sesion({"jsonrpc": "2.0", "id": 5, "method": "tools/call",
                          "params": {"name": "box_estado", "arguments": {"nodo": "caja-rota"}}})
        self.assertTrue(rs[0]["result"]["isError"])
        self.assertEqual(rs[0]["result"]["structuredContent"]["error"]["codigo"], "nodo_inalcanzable")

    def test_argumento_faltante(self):
        rs = self.sesion({"jsonrpc": "2.0", "id": 6, "method": "tools/call",
                          "params": {"name": "box_estado", "arguments": {}}})
        self.assertEqual(rs[0]["error"]["code"], -32602)


if __name__ == "__main__":
    unittest.main()
