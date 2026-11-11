import argparse
import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest import mock

from _comun import cargar_boxctl

bx = cargar_boxctl()
LIM = {**bx.DEFAULTS, "GC_HUERFANO_MIN": "10", "SUITE_MAX_MIN": "45", "SUITE_MAX_GB": "6",
       "MEM_ALERTA_PCT": "80", "MEM_CORTE_PCT": "90"}
H = 3600


def P(pid, ppid, comm, args=None, edad=H, rss_mb=100, cwd=""):
    return {"pid": pid, "ppid": ppid, "comm": comm, "args": args or comm, "edadS": edad,
            "rssKb": rss_mb * 1024, "cwd": cwd}


BASE = [P(1, 0, "docker-init"), P(7, 1, "sleep", "sleep infinity"),
        P(50, 1, "MainThread", "/home/agent/.orca-remote/runtimes/node-x/bin/node orcad"),
        P(100, 50, "claude", "claude --plugin-dir x", cwd="/ws/a")]


def plan(procs, usada=4, limite=12):
    return bx.planear_gc_procesos(BASE + procs, LIM, usada, limite)


class GcProcesos(unittest.TestCase):
    def tipos(self, r):
        return [(a["tipo"], a["pid"]) for a in r["acciones"]]

    def test_nunca_toca_protegidos_ni_lo_que_cuelga_de_un_claude(self):
        r = plan([P(101, 100, "node", "node jest", edad=10 * H, rss_mb=9000)], usada=11.5)
        pids = {p for a in r["acciones"] for p in a["pids"]}
        self.assertTrue(pids.isdisjoint({1, 7, 50, 100}))

    def test_huerfano_viejo_sin_agente_se_mata_con_su_subarbol(self):
        r = plan([P(200, 1, "node", "node jest", cwd="/ws/muerto"), P(201, 200, "node", "jest-worker")])
        self.assertEqual(self.tipos(r), [("huerfano", 200)])
        self.assertEqual(r["acciones"][0]["pids"], [200, 201])

    def test_huerfano_reciente_se_respeta(self):
        self.assertEqual(plan([P(200, 1, "node", edad=60)])["acciones"], [])

    def test_huerfano_en_el_worktree_de_un_claude_vivo_se_respeta(self):
        self.assertEqual(plan([P(200, 1, "node", "npm run dev", cwd="/ws/a/front")])["acciones"], [])

    def test_lanzado_por_docker_exec_no_es_huerfano(self):
        self.assertEqual(plan([P(300, 0, "bash", "bash -l")])["acciones"], [])

    def test_suite_que_pasa_el_tope_de_tiempo(self):
        r = plan([P(400, 0, "bash", "bash -lc x"), P(401, 400, "node", "node node_modules/.bin/jest", edad=50 * 60)])
        self.assertEqual(self.tipos(r), [("suite-tiempo", 401)])

    def test_suite_que_pasa_el_tope_de_ram_cuenta_sus_workers(self):
        r = plan([P(400, 0, "bash"), P(401, 400, "node", "npx jest", edad=60, rss_mb=500),
                  P(402, 401, "node", "jest-worker/processChild.js", rss_mb=3000),
                  P(403, 401, "node", "jest-worker/processChild.js", rss_mb=3000)])
        self.assertEqual(self.tipos(r), [("suite-ram", 401)])

    def test_memoria_al_corte_mata_la_suite_mas_grande_y_alerta(self):
        r = plan([P(400, 0, "bash"), P(401, 400, "node", "npx jest", edad=60, rss_mb=1000),
                  P(500, 0, "bash"), P(501, 500, "node", "npx vitest", edad=60, rss_mb=3000)], usada=11, limite=12)
        self.assertEqual(self.tipos(r), [("memoria-alta", 501)])
        self.assertTrue(r["alerta"])

    def test_debajo_del_corte_solo_alerta(self):
        r = plan([P(400, 0, "bash"), P(401, 400, "node", "npx jest", edad=60)], usada=10, limite=12)
        self.assertEqual((r["acciones"], r["alerta"]), ([], True))


class Salvar(unittest.TestCase):
    def setUp(self):
        self.d = Path(tempfile.mkdtemp())
        g = lambda *a: subprocess.run(["git", "-C", str(self.d), *a], check=True, capture_output=True, text=True)
        self.g = g
        g("init", "-q", "-b", "feature/x"); g("config", "user.email", "t@x"); g("config", "user.name", "t")
        (self.d / "a.txt").write_text("uno\n"); g("add", "."); g("commit", "-qm", "base")
        (self.d / "a.txt").write_text("dos\n")            # modificado sin stage
        (self.d / "nuevo.txt").write_text("nuevo\n")      # no trackeado
        (self.d / "b.txt").write_text("staged\n"); g("add", "b.txt")

    def test_snapshot_no_toca_head_index_ni_archivos(self):
        antes = (self.g("rev-parse", "HEAD").stdout, self.g("status", "--porcelain").stdout)
        with mock.patch.object(bx, "COMO_AGENT", []):
            s = bx.snapshot_wip(str(self.d), {"GIT_NAME": "Operador", "GIT_EMAIL": "t@x"}, "20261103-1200")
        self.assertEqual((self.g("rev-parse", "HEAD").stdout, self.g("status", "--porcelain").stdout), antes)
        self.assertEqual(s["ref"], "refs/wip/feature/x/20261103-1200")
        arbol = self.g("ls-tree", "-r", "--name-only", s["ref"]).stdout.split()
        self.assertEqual(sorted(arbol), ["a.txt", "b.txt", "nuevo.txt"])
        self.assertEqual(self.g("show", f"{s['ref']}:a.txt").stdout, "dos\n")
        self.assertEqual(self.g("rev-parse", f"{s['ref']}^").stdout, antes[0])
        self.assertNotIn("claude", self.g("log", "-1", "--format=%B", s["ref"]).stdout.lower())

    def test_dry_run_no_escribe_refs(self):
        cfg = {"WORKSPACE": str(self.d.parent)}
        with mock.patch.object(bx, "COMO_AGENT", []), mock.patch.object(bx, "leer_env", return_value=cfg), \
                mock.patch.object(bx, "inspeccionar_caja", return_value=None), \
                mock.patch.object(bx, "listar_worktrees", return_value=[str(self.d)]):
            r = bx.op_salvar(argparse.Namespace(aplicar=False))
        self.assertEqual(len(r["salvados"]), 1)
        self.assertEqual(self.g("for-each-ref", "refs/wip").stdout, "")

    def test_aplicar_con_la_caja_caida_deja_el_ref_local(self):
        cfg = {"WORKSPACE": str(self.d.parent), "GIT_NAME": "T", "GIT_EMAIL": "t@x"}
        with mock.patch.object(bx, "COMO_AGENT", []), mock.patch.object(bx, "leer_env", return_value=cfg), \
                mock.patch.object(bx, "inspeccionar_caja", return_value=None), \
                mock.patch.object(bx, "bitacora"), \
                mock.patch.object(bx, "listar_worktrees", return_value=[str(self.d)]):
            r = bx.op_salvar(argparse.Namespace(aplicar=True))
        self.assertFalse(r["salvados"][0]["pusheado"])
        self.assertIn("refs/wip/feature/x/", self.g("for-each-ref", "refs/wip").stdout)


class CajaReiniciarSalva(unittest.TestCase):
    def test_salva_antes_de_borrar_y_frena_si_salvar_falla(self):
        orden = []
        with mock.patch.object(bx, "leer_env", return_value={}), \
                mock.patch.object(bx, "inspeccionar_caja", return_value={"x": 1}), \
                mock.patch.object(bx, "contar_agentes", return_value=0), \
                mock.patch.object(bx, "op_salvar", side_effect=lambda a: orden.append("salvar") or
                                  {"salvados": [], "fallos": [{"path": "/w", "mensaje": "x"}]}), \
                mock.patch.object(bx, "sh", side_effect=lambda c, **k: orden.append(c[1])):
            with self.assertRaises(bx.BoxError):
                bx.op_caja_reiniciar(argparse.Namespace(forzar=False))
        self.assertEqual(orden, ["salvar"])


if __name__ == "__main__":
    unittest.main()
