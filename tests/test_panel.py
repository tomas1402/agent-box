import json
import tempfile
import threading
import unittest
import urllib.request
from pathlib import Path
from unittest import mock

from _comun import cargar_boxctl

bx = cargar_boxctl()


WT = {
    "worktreeId": "repo::/tmp/frontends/us-1",
    "repo": "frontends",
    "path": "/tmp/frontends/us-1",
    "branch": "refs/heads/feature/us-1",
    "displayName": "us 1",
    "workspaceStatus": "in-progress",
    "hostId": "local",
    "isArchived": False,
    "isMainWorktree": False,
    "liveTerminalCount": 1,
    "lastActivityAt": 1794102069230,
    "linkedIssue": {"title": "US 123 (titulo de ejemplo)"},
    "unread": True,
}


class Proyeccion(unittest.TestCase):
    def test_lee_el_json_real_de_orca(self):
        fila = bx.proyectar_worktree(WT)
        self.assertEqual(fila["id"], "repo::/tmp/frontends/us-1")
        self.assertEqual(fila["repo"], "frontends")
        self.assertEqual(fila["rama"], "feature/us-1")
        self.assertEqual(fila["seguimiento"], "US 123 (titulo de ejemplo)")
        self.assertTrue(fila["actividad"].startswith("2026-"))

    def test_sin_terminal_no_cuenta_como_en_curso(self):
        quieto = bx.proyectar_worktree({**WT, "liveTerminalCount": 0, "repo": "docproxy"})
        vivo = bx.proyectar_worktree(WT)
        sesiones, abiertos = bx.partir_sesiones([quieto, vivo])
        self.assertEqual([f["repo"] for f in sesiones], ["frontends"])
        self.assertEqual([f["repo"] for f in abiertos], ["docproxy"])

    def test_el_clon_principal_quieto_no_entra(self):
        principal = {**WT, "isMainWorktree": True, "liveTerminalCount": 0, "repo": "yo"}
        activo = {**WT}
        self.assertEqual([f["repo"] for f in bx.en_curso([
            bx.proyectar_worktree(principal), bx.proyectar_worktree(activo),
        ])], ["frontends"])

    def test_reloj_lista_esperando_y_un_archivo_roto_no_tira_el_panel(self):
        with tempfile.TemporaryDirectory() as d:
            Path(d, "123.json").write_text(json.dumps({
                "id": "123", "fase": "frenado", "worktree": "/wt",
                "tracker": {"repo": "frontends"}, "ultimoGate": "test",
                "esperando": ["criterio de e2e"], "actualizado": "2026-11-11T12:00:00Z",
            }))
            Path(d, "roto.json").write_text("{")
            filas, avisos = bx.leer_reloj(Path(d))
        self.assertEqual(filas[0]["repo"], "frontends")
        self.assertEqual(filas[0]["esperando"], ["criterio de e2e"])
        self.assertTrue(avisos)

    def test_panel_json_sigue_si_orca_falla(self):
        with mock.patch.object(bx, "orca", side_effect=bx.BoxError("error_interno", "orca caido")):
            datos = bx.armar_panel(Path("/no/existe-reloj"))
        self.assertEqual(datos["sesiones"], [])
        self.assertTrue(any("orca caido" in a for a in datos["avisos"]))
        self.assertTrue(any("boxd" in a for a in datos["avisos"]))

    def test_servir_responde_html_y_json(self):
        payload = {"ts": "t", "sesiones": [bx.proyectar_worktree(WT)], "reloj": [], "avisos": []}
        httpd = bx.panel_httpd(lambda: payload, 0)
        hilo = threading.Thread(target=httpd.serve_forever, daemon=True)
        hilo.start()
        puerto = httpd.server_address[1]
        try:
            html = urllib.request.urlopen(f"http://127.0.0.1:{puerto}/", timeout=2).read()
            api = json.load(urllib.request.urlopen(f"http://127.0.0.1:{puerto}/api/panel", timeout=2))
        finally:
            httpd.shutdown()
            hilo.join(timeout=2)
        self.assertIn(b"Cajas", html)
        self.assertEqual(api["sesiones"][0]["repo"], "frontends")


class PanelEtapa(unittest.TestCase):
    """La caja expone su etapa y se junta con la sesion de Orca por el path del worktree."""

    def test_etapa_corrida_y_union_con_la_sesion(self):
        import json, tempfile
        from pathlib import Path
        from unittest import mock
        with tempfile.TemporaryDirectory() as d:
            wt = Path(d, "wt"); wt.mkdir()
            Path(d, "9.json").write_text(json.dumps({
                "id": "9", "fase": "trabajando", "worktree": str(wt), "tracker": {"repo": "repo-demo"},
                "actividad": {"etapa": "e2e", "detalle": "e2e/alta.spec.ts", "desde": "2026-11-11T10:00:00Z",
                              "archivos": {"codigo": 3, "e2e": 1}},
                "corrida": {"tipo": "paso", "inicio": "2026-11-11T09:59:00Z", "ms": 1200, "codigo": "",
                            "gates": [{"nombre": "lint", "exit": 0, "ms": 1200, "verde": True}]}}))
            orca = {"worktrees": [{"worktreeId": "w1", "path": str(wt), "branch": "refs/heads/feature/us-9",
                                   "repo": "repo-demo", "displayName": "us 9", "liveTerminalCount": 1}]}
            with mock.patch.object(bx, "orca", return_value=orca):
                p = bx.armar_panel(Path(d))
        fila = p["reloj"][0]
        self.assertEqual((fila["etapa"], fila["detalle"], fila["archivos"]["codigo"]), ("e2e", "e2e/alta.spec.ts", 3))
        self.assertEqual(fila["corrida"]["gates"], [{"nombre": "lint", "verde": True, "ms": 1200}])
        self.assertEqual(fila["sesion"]["nombre"], "us 9")
        self.assertEqual(p["sesiones"][0]["caja"], {"wi": "9", "fase": "trabajando", "etapa": "e2e"})

    def test_cache_arma_una_sola_vez(self):
        n = []
        leer = bx.con_cache(lambda: n.append(1) or {"x": len(n)}, segundos=60)
        self.assertEqual([leer()["x"], leer()["x"], leer()["x"]], [1, 1, 1])
