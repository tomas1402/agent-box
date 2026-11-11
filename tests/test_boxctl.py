import contextlib
import io
import json
import re
import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest import mock

from _comun import RAIZ, cargar_boxctl

bx = cargar_boxctl()
LIM = dict(bx.DEFAULTS)
GIB = bx.GIB


def correr(argv):
    out = io.StringIO()
    with contextlib.redirect_stdout(out), contextlib.redirect_stderr(io.StringIO()):
        rc = bx.main(argv)
    return rc, out.getvalue()


def cfg_ejemplo(nodo="nodo-a"):
    with tempfile.NamedTemporaryFile("w", delete=False, suffix=".env") as f:
        f.write((RAIZ / "config" / f"box.env.{nodo}.example").read_text())
    return Path(f.name)


class Capacidad(unittest.TestCase):
    def test_ejemplo_del_informe_13gb_7_agentes_da_una_suite(self):
        self.assertEqual(bx.capacidad(13, 7, 2, LIM)["suitesCaben"], 1)

    def test_caja_vacia_12gb(self):
        c = bx.capacidad(12, 0, 11, LIM)
        self.assertEqual(c["suitesCaben"], 2)        # (12 - 0 - 1) / 4 = 2,75
        self.assertEqual(c["workersCaben"], 4)       # por RAM 11; tope WORKERS_MAX=4

    def test_nunca_menos_de_una_suite(self):
        self.assertEqual(bx.capacidad(4, 10, 0, LIM)["suitesCaben"], 1)

    def test_tope_de_suites(self):
        self.assertEqual(bx.capacidad(64, 0, 60, LIM)["suitesCaben"], 3)

    def test_sin_ram_para_suite_no_admite_worker(self):
        self.assertEqual(bx.capacidad(12, 1, 4.0, LIM)["workersCaben"], 0)
        self.assertEqual(bx.capacidad(12, 1, 4.6, LIM)["workersCaben"], 1)

    def test_tope_de_workers_descuenta_los_vivos(self):
        self.assertEqual(bx.capacidad(12, 3, 11, LIM)["workersCaben"], 1)
        self.assertEqual(bx.capacidad(12, 5, 11, LIM)["workersCaben"], 0)


class Deriva(unittest.TestCase):
    def setUp(self):
        self.path = cfg_ejemplo()
        self.cfg = bx.leer_env(self.path)

    def caja(self, **cambios):
        hc = {"Memory": 12 * GIB, "MemorySwap": 12 * GIB, "NanoCpus": int(10e9), "Runtime": "runsc",
              "Init": True, "PidsLimit": 4096, "NetworkMode": "agent-box-net"}
        hc.update(cambios)
        return {"HostConfig": hc, "Config": {"Labels": {"agent-box.config": "h"}},
                "Mounts": [{"Destination": "/home/agent/.orca"}]}

    def test_caja_igual_a_box_env_no_tiene_deriva(self):
        self.assertEqual(bx.deriva(self.cfg, self.caja(), "h"), [])

    def test_la_caja_reporta_memoria_swap_cpus_red_y_orca(self):
        viva = self.caja(Memory=13 * GIB, MemorySwap=26 * GIB, NanoCpus=int(11.5e9), NetworkMode="bridge")
        viva["Mounts"] = []
        campos = {d["campo"] for d in bx.deriva(self.cfg, viva, "h")}
        self.assertEqual(campos, {"memoria", "swap", "cpus", "red", "orcaPersistente"})

    def test_sin_init_se_reporta(self):
        self.assertEqual([d["campo"] for d in bx.deriva(self.cfg, self.caja(Init=False), "h")], ["init"])

    def test_box_env_incompleto_es_config_invalida(self):
        p = Path(tempfile.mktemp())
        p.write_text("NODO=nodo-a\nMEM_GB=12\n")
        with self.assertRaises(bx.BoxError) as e:
            bx.leer_env(p)
        self.assertEqual(e.exception.codigo, "config_invalida")


class Contrato(unittest.TestCase):
    def test_codigos_y_exit_codes_coinciden_con_docs(self):
        doc = (RAIZ / "docs" / "contrato.md").read_text()
        tabla = dict(re.findall(r"^\| `([a-z_]+)` \| (\d+) \|", doc, re.M))
        self.assertEqual({k: int(v) for k, v in tabla.items()}, bx.ERRORES)

    def test_exit_codes_unicos(self):
        self.assertEqual(len(set(bx.ERRORES.values())), len(bx.ERRORES))

    def test_schema_lista_los_mismos_codigos(self):
        sch = json.loads((RAIZ / "docs" / "contrato.schema.json").read_text())
        self.assertEqual(set(sch["$defs"]["error"]["properties"]["codigo"]["enum"]), set(bx.ERRORES))

    def test_version_en_json(self):
        rc, out = correr(["--json", "version"])
        d = json.loads(out)
        self.assertEqual((rc, d["ok"], d["contrato"]), (0, True, bx.CONTRATO))

    def test_verbo_de_nodo_fuera_de_un_nodo_devuelve_json_y_exit_2(self):
        with mock.patch.object(bx, "BOX_ENV", Path("/no/existe")):
            rc, out = correr(["--json", "estado"])
        d = json.loads(out)
        self.assertEqual((rc, d["ok"], d["error"]["codigo"]), (2, False, "operacion_invalida"))

    def test_argumentos_invalidos_tambien_devuelven_json(self):
        rc, out = correr(["--json", "worker", "alta"])
        self.assertEqual((rc, json.loads(out)["error"]["codigo"]), (2, "operacion_invalida"))

    def test_nodo_desconocido(self):
        with tempfile.TemporaryDirectory() as d:
            p = Path(d, "nodos.json")
            p.write_text((RAIZ / "config" / "nodos.json.example").read_text())
            with mock.patch.object(bx, "NODOS_JSON", p), mock.patch.object(bx, "BOX_ENV", Path("/no/existe")):
                rc, out = correr(["--json", "--nodo", "marte", "estado"])
        self.assertEqual((rc, json.loads(out)["error"]["codigo"]), (9, "nodo_desconocido"))


class Remoto(unittest.TestCase):
    N = {"nombre": "nodo-a", "ssh": "nodo-a-admin", "orcaEnvironment": "nodo-a"}

    def cp(self, rc, out="", err=""):
        return subprocess.CompletedProcess([], rc, out, err)

    def test_ssh_caido_es_nodo_inalcanzable(self):
        with mock.patch.object(bx, "sh", return_value=self.cp(255, err="timeout")):
            with self.assertRaises(bx.BoxError) as e:
                bx.remoto(self.N, ["estado"])
        self.assertEqual(e.exception.codigo, "nodo_inalcanzable")

    def test_error_remoto_se_propaga_con_su_codigo(self):
        sobre = {"ok": False, "error": {"codigo": "caja_ocupada", "mensaje": "hay 6 agentes"}}
        with mock.patch.object(bx, "sh", return_value=self.cp(6, json.dumps(sobre))):
            with self.assertRaises(bx.BoxError) as e:
                bx.remoto(self.N, ["caja-reiniciar"])
        self.assertEqual(e.exception.codigo, "caja_ocupada")

    def test_argumentos_van_quoteados(self):
        with mock.patch.object(bx, "sh", return_value=self.cp(0, '{"ok":true,"datos":{}}')) as m:
            bx.remoto(self.N, ["podar", "/home/agent/workspace/a b;rm"])
        self.assertIn("'/home/agent/workspace/a b;rm'", m.call_args[0][0][-1])


class Reenvio(unittest.TestCase):
    def test_nodo_con_igual_tambien_se_reenvia_sin_el_flag(self):
        with mock.patch.object(bx, "BOX_ENV", Path("/no/existe")), \
                mock.patch.object(bx, "nodo", return_value={"nombre": "nodo-a", "ssh": "c"}), \
                mock.patch.object(bx, "remoto", return_value={}) as r:
            rc, _ = correr(["--json", "--nodo=nodo-a", "logs", "-n", "5"])
        self.assertEqual((rc, r.call_args[0][1]), (0, ["logs", "-n", "5"]))


class CajaReiniciar(unittest.TestCase):
    def test_con_agentes_vivos_rechaza_y_no_toca_docker(self):
        p = cfg_ejemplo()
        with mock.patch.object(bx, "BOX_ENV", p), mock.patch.object(bx.os, "geteuid", return_value=0), \
                mock.patch.object(bx, "inspeccionar_caja", return_value={"x": 1}), \
                mock.patch.object(bx, "contar_agentes", return_value=6), \
                mock.patch.object(bx, "sh") as sh:
            rc, out = correr(["--json", "caja-reiniciar"])
        d = json.loads(out)
        self.assertEqual((rc, d["error"]["codigo"], d["error"]["detalle"]["agentes"]), (6, "caja_ocupada", 6))
        sh.assert_not_called()

    def test_sin_root_es_permiso(self):
        p = cfg_ejemplo()
        with mock.patch.object(bx, "BOX_ENV", p), mock.patch.object(bx.os, "geteuid", return_value=1000):
            rc, out = correr(["--json", "estado"])
        self.assertEqual((rc, json.loads(out)["error"]["codigo"]), (8, "permiso"))


class Pool(unittest.TestCase):
    def nodo(self, nombre, workers, suites=1, ok=True):
        if not ok:
            return {"nombre": nombre, "alcanzable": False, "orcaEnvironment": nombre}
        return {"nombre": nombre, "alcanzable": True, "orcaEnvironment": nombre,
                "estado": {"capacidad": {"workersCaben": workers, "suitesCaben": suites}}}

    def test_elige_el_de_mas_lugar(self):
        e = bx.elegir_nodo([self.nodo("nodo-a", 1), self.nodo("nodo-b", 3), self.nodo("x", 0, ok=False)])
        self.assertEqual(e["nombre"], "nodo-b")

    def test_empate_desempata_por_suites(self):
        e = bx.elegir_nodo([self.nodo("nodo-a", 2, 1), self.nodo("nodo-b", 2, 2)])
        self.assertEqual(e["nombre"], "nodo-b")

    def test_sin_lugar_es_capacidad_insuficiente(self):
        with self.assertRaises(bx.BoxError) as e:
            bx.elegir_nodo([self.nodo("nodo-a", 0), self.nodo("nodo-b", 0, ok=False)])
        self.assertEqual(e.exception.codigo, "capacidad_insuficiente")

    def test_alta_sin_aplicar_no_llama_a_orca(self):
        nodos = {"nodos": [self.nodo("nodo-a", 2)]}
        with mock.patch.object(bx, "op_nodos", return_value=nodos), mock.patch.object(bx, "orca") as o:
            rc, out = correr(["--json", "worker", "alta", "--repo", "repo-demo", "--rama", "feature/x"])
        d = json.loads(out)
        self.assertEqual((rc, d["datos"]["nodo"], d["datos"]["aplicado"]), (0, "nodo-a", False))
        o.assert_not_called()

    def test_alta_con_repo_no_registrado(self):
        nodos = {"nodos": [self.nodo("nodo-a", 2)]}
        with mock.patch.object(bx, "op_nodos", return_value=nodos), \
                mock.patch.object(bx, "orca", return_value={"repos": [{"displayName": "otro", "id": "1"}]}):
            rc, out = correr(["--json", "worker", "alta", "--repo", "repo-demo", "--rama", "x", "--aplicar"])
        self.assertEqual((rc, json.loads(out)["error"]["codigo"]), (11, "repo_no_registrado"))


if __name__ == "__main__":
    unittest.main()
