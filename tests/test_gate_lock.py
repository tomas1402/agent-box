"""gate-lock v2: slots por RAM (misma formula que boxctl), tope, timeout, slot huerfano.

free y pgrep se reemplazan con un shim en el PATH: no hay variables de entorno para tocar la
cuenta (un agente podria usarlas para saltarse la cola).
"""
import os
import re
import shutil
import subprocess
import tempfile
import time
import unittest
from pathlib import Path

from _comun import RAIZ, cargar_boxctl

bx = cargar_boxctl()
GL = str(RAIZ / "caja" / "gate-lock.sh")


class GateLock(unittest.TestCase):
    def setUp(self):
        self.grupo = f"unittest-{os.getpid()}-{self._testMethodName}"
        self.base = Path("/tmp/harness-gate-lock") / self.grupo
        self.shim = Path(tempfile.mkdtemp())

    def tearDown(self):
        shutil.rmtree(self.base, ignore_errors=True)
        shutil.rmtree(self.shim, ignore_errors=True)

    def entorno(self, mem_gb, agentes, disponible_gb=None):
        disp = int((mem_gb if disponible_gb is None else disponible_gb) * 1024)
        (self.shim / "free").write_text(
            "#!/bin/sh\necho '               total        used        free      shared  buff/cache   available'\n"
            f"echo 'Mem:    {int(mem_gb * 1024)}  100 100 0 0 {disp}'\n")
        (self.shim / "pgrep").write_text(f"#!/bin/sh\necho {agentes}\n")
        for f in ("free", "pgrep"):
            (self.shim / f).chmod(0o755)
        return {**os.environ, "PATH": f"{self.shim}:{os.environ['PATH']}", "GATE_LOCK_POLL": "0.2"}

    def slots(self, mem_gb, agentes, tope="auto", disponible_gb=None):
        r = subprocess.run([GL, self.grupo, tope, "--", "true"], capture_output=True, text=True,
                           env=self.entorno(mem_gb, agentes, disponible_gb), timeout=30)
        self.assertEqual(r.returncode, 0, r.stderr)
        return int(re.search(r"slot-\d+/(\d+)", r.stderr).group(1))

    def test_misma_formula_que_boxctl(self):
        for mem, ag in [(13, 7), (12, 0), (12, 3), (8, 2), (4, 10), (64, 0), (16, 4)]:
            with self.subTest(mem=mem, agentes=ag):
                esperado = bx.capacidad(mem, ag, mem, bx.DEFAULTS)["suitesCaben"]
                self.assertEqual(self.slots(mem, ag), esperado)

    def test_admision_por_ram_libre_real(self):
        # techo 3 (64 GB), pero solo 9 GB libres: (9 - 1) / 4 = 2 suites
        self.assertEqual(self.slots(64, 0, disponible_gb=9), 2)
        # sin RAM libre igual entra una (la primera nunca se queda esperando)
        self.assertEqual(self.slots(64, 0, disponible_gb=0.5), 1)

    def test_el_tope_numerico_acota(self):
        self.assertEqual(self.slots(64, 0, "1"), 1)
        self.assertEqual(self.slots(12, 0, "9"), 2)

    def test_tope_invalido(self):
        r = subprocess.run([GL, self.grupo, "x", "--", "true"], capture_output=True, text=True,
                           env=self.entorno(12, 0))
        self.assertEqual(r.returncode, 2)

    def test_devuelve_el_exit_del_comando_y_libera(self):
        r = subprocess.run([GL, self.grupo, "1", "--", "sh", "-c", "exit 7"], capture_output=True,
                           env=self.entorno(12, 0))
        self.assertEqual(r.returncode, 7)
        self.assertEqual(list(self.base.glob("slot-*")), [])

    def test_sin_slot_sale_75_al_vencer(self):
        env = self.entorno(12, 0)
        ocupa = subprocess.Popen([GL, self.grupo, "1", "--", "sleep", "5"], env=env,
                                 stderr=subprocess.DEVNULL)
        try:
            for _ in range(50):
                if (self.base / "slot-1" / "pid").exists():
                    break
                time.sleep(0.1)
            r = subprocess.run([GL, self.grupo, "1", "--", "true"], capture_output=True, text=True,
                               env={**env, "GATE_LOCK_TIMEOUT": "1"}, timeout=20)
            self.assertEqual(r.returncode, 75)
        finally:
            ocupa.kill(); ocupa.wait()

    def test_recupera_slot_de_un_proceso_muerto(self):
        p = subprocess.Popen(["true"]); p.wait()
        (self.base / "slot-1").mkdir(parents=True)
        (self.base / "slot-1" / "pid").write_text(str(p.pid))
        self.assertEqual(self.slots(12, 0, "1"), 1)
        self.assertEqual([x.name for x in self.base.iterdir()], [])


FLOCK_FALSO = """#!/usr/bin/env python3
# flock(1) minimo para macOS: solo la forma `flock -n <fd>` que usa gate-lock.
import fcntl, sys
try:
    fcntl.flock(int(sys.argv[2]), fcntl.LOCK_EX | fcntl.LOCK_NB)
except OSError:
    sys.exit(1)
"""


class GateLockFlock(GateLock):
    """Los mismos casos, en modo flock (el de la caja), mas: un dueño muerto no deja el slot tomado."""

    def entorno(self, mem_gb, agentes, disponible_gb=None):
        env = super().entorno(mem_gb, agentes, disponible_gb)
        (self.shim / "flock").write_text(FLOCK_FALSO); (self.shim / "flock").chmod(0o755)
        return env

    def test_sin_slot_sale_75_al_vencer(self):
        # En modo flock no existe slot-1/pid: se espera el aviso "tomado" por stderr.
        env = self.entorno(12, 0)
        ocupa = subprocess.Popen([GL, self.grupo, "1", "--", "sleep", "30"], env=env, stderr=subprocess.PIPE)
        try:
            ocupa.stderr.readline()
            r = subprocess.run([GL, self.grupo, "1", "--", "true"], capture_output=True, text=True,
                               env={**env, "GATE_LOCK_TIMEOUT": "1"}, timeout=20)
            self.assertEqual(r.returncode, 75)
        finally:
            subprocess.run(["pkill", "-KILL", "-P", str(ocupa.pid)]); ocupa.kill(); ocupa.wait()
            ocupa.stderr.close()

    def test_recupera_slot_de_un_proceso_muerto(self):
        env = self.entorno(12, 0)
        ocupa = subprocess.Popen([GL, self.grupo, "1", "--", "sleep", "30"], env=env, stderr=subprocess.PIPE)
        ocupa.stderr.readline()  # "slot-1/1 ... tomado"
        r = subprocess.run([GL, self.grupo, "1", "--", "true"], capture_output=True, text=True,
                           env={**env, "GATE_LOCK_TIMEOUT": "1"}, timeout=20)
        self.assertEqual(r.returncode, 75)
        subprocess.run(["pkill", "-KILL", "-P", str(ocupa.pid)]); ocupa.kill(); ocupa.wait()
        self.assertEqual(self.slots(12, 0, "1"), 1)

    def test_devuelve_el_exit_del_comando_y_libera(self):
        env = self.entorno(12, 0)
        r = subprocess.run([GL, self.grupo, "1", "--", "sh", "-c", "exit 7"], capture_output=True, env=env)
        self.assertEqual(r.returncode, 7)
        # el .lock queda como archivo, pero libre: otro lo toma sin esperar
        self.assertEqual(self.slots(12, 0, "1"), 1)


if __name__ == "__main__":
    unittest.main()
