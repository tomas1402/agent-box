import importlib.machinery
import importlib.util
from pathlib import Path

RAIZ = Path(__file__).resolve().parent.parent


def cargar_boxctl():
    ruta = str(RAIZ / "boxctl" / "boxctl")
    loader = importlib.machinery.SourceFileLoader("boxctl_mod", ruta)
    spec = importlib.util.spec_from_loader("boxctl_mod", loader)
    mod = importlib.util.module_from_spec(spec)
    loader.exec_module(mod)
    return mod
