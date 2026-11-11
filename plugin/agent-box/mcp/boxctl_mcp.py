#!/usr/bin/env python3
"""Servidor MCP (stdio) de la box de agentes: envoltorio fino de `boxctl --json`.

No tiene logica propia: cada tool arma argv, llama a boxctl y devuelve el sobre JSON tal cual
(structuredContent) y como texto. Los errores del contrato vuelven con isError=true y el codigo.

A proposito NO expone verbos destructivos (caja-reiniciar, limpiar --aplicar, firewall aplicar/
quitar, worker baja --aplicar): esos los corre un humano o un agente con OK explicito de quien opera,
por la CLI. `worker_alta` crea solo con aplicar=true.

Transporte: JSON-RPC 2.0, un mensaje por linea en stdin/stdout (MCP stdio). Sin dependencias.
BOXCTL = ruta a boxctl (default: `boxctl` del PATH).
"""
from __future__ import annotations

import json
import os
import subprocess
import sys

PROTOCOLO = "2025-06-18"
BOXCTL = os.environ.get("BOXCTL", "boxctl")

NODO = {"type": "string", "description": "nodo de nodos.json (p. ej. nodo-a, nodo-b)"}
TOOLS = {
    "box_estado": {
        "description": "Estado de un nodo: host, caja, capacidad (workersCaben, suitesCaben), deriva de config.",
        "inputSchema": {"type": "object", "properties": {"nodo": NODO}, "required": ["nodo"]},
        "argv": lambda a: ["--nodo", a["nodo"], "estado"],
    },
    "box_nodos": {
        "description": "Estado de todos los nodos del pool en una sola llamada.",
        "inputSchema": {"type": "object", "properties": {}},
        "argv": lambda a: ["nodos"],
    },
    "box_suites": {
        "description": "Cola de suites de test del nodo: slots por RAM, ocupados y libres.",
        "inputSchema": {"type": "object", "properties": {"nodo": NODO}, "required": ["nodo"]},
        "argv": lambda a: ["--nodo", a["nodo"], "suites"],
    },
    "box_limpiar_plan": {
        "description": "Dry-run de la poda: worktrees por balde y cuales serian podables. No borra nada.",
        "inputSchema": {"type": "object", "properties": {"nodo": NODO}, "required": ["nodo"]},
        "argv": lambda a: ["--nodo", a["nodo"], "limpiar"],
    },
    "box_logs": {
        "description": "Ultimas operaciones auditadas del nodo (JSON por linea).",
        "inputSchema": {"type": "object", "properties": {"nodo": NODO, "n": {"type": "integer", "default": 30}},
                        "required": ["nodo"]},
        "argv": lambda a: ["--nodo", a["nodo"], "logs", "-n", str(int(a.get("n", 30)))],
    },
    "box_workers": {
        "description": "Workers (worktrees de Orca) de todos los nodos con el estado de sus agentes.",
        "inputSchema": {"type": "object", "properties": {}},
        "argv": lambda a: ["worker", "lista"],
    },
    "box_worker_alta": {
        "description": "Elige nodo por capacidad y crea el worktree en Orca. Sin aplicar=true solo devuelve el plan.",
        "inputSchema": {"type": "object", "properties": {
            "repo": {"type": "string"}, "rama": {"type": "string"}, "wi": {"type": "string"},
            "nodo": {"type": "string", "default": "auto"}, "base": {"type": "string", "default": "origin/main"},
            "aplicar": {"type": "boolean", "default": False}}, "required": ["repo", "rama"]},
        "argv": lambda a: (["--nodo", a.get("nodo", "auto"), "worker", "alta", "--repo", a["repo"], "--rama", a["rama"],
                            "--base", a.get("base", "origin/main")]
                           + (["--wi", str(a["wi"])] if a.get("wi") else [])
                           + (["--aplicar"] if a.get("aplicar") else [])),
    },
}


def llamar(argv: list[str]) -> tuple[dict, bool]:
    try:
        r = subprocess.run([BOXCTL, "--json", *argv], capture_output=True, text=True, timeout=600)
    except FileNotFoundError:
        return {"ok": False, "error": {"codigo": "dependencia_faltante", "mensaje": f"no se encontro {BOXCTL}"}}, True
    try:
        sobre = json.loads(r.stdout)
    except ValueError:
        sobre = {"ok": False, "error": {"codigo": "error_interno", "mensaje": (r.stderr or r.stdout)[-500:]}}
    return sobre, not sobre.get("ok", False)


def responder(id_, result=None, error=None) -> None:
    msg = {"jsonrpc": "2.0", "id": id_}
    msg["error" if error else "result"] = error or result
    sys.stdout.write(json.dumps(msg, ensure_ascii=False) + "\n")
    sys.stdout.flush()


def manejar(m: dict) -> None:
    metodo, id_ = m.get("method"), m.get("id")
    if id_ is None:  # notificacion (p. ej. notifications/initialized): no se responde
        return
    if metodo == "initialize":
        responder(id_, {"protocolVersion": m.get("params", {}).get("protocolVersion", PROTOCOLO),
                        "capabilities": {"tools": {}},
                        "serverInfo": {"name": "agent-box", "version": "0.1.0"}})
    elif metodo == "ping":
        responder(id_, {})
    elif metodo == "tools/list":
        responder(id_, {"tools": [{"name": k, "description": v["description"], "inputSchema": v["inputSchema"]}
                                  for k, v in TOOLS.items()]})
    elif metodo == "tools/call":
        p = m.get("params", {})
        t = TOOLS.get(p.get("name"))
        if not t:
            responder(id_, error={"code": -32602, "message": f"tool desconocida: {p.get('name')}"})
            return
        try:
            argv = t["argv"](p.get("arguments") or {})
        except (KeyError, ValueError) as e:
            responder(id_, error={"code": -32602, "message": f"argumento faltante o invalido: {e}"})
            return
        sobre, es_error = llamar(argv)
        responder(id_, {"content": [{"type": "text", "text": json.dumps(sobre, ensure_ascii=False)}],
                        "structuredContent": sobre, "isError": es_error})
    else:
        responder(id_, error={"code": -32601, "message": f"metodo no soportado: {metodo}"})


def main() -> None:
    for linea in sys.stdin:
        linea = linea.strip()
        if not linea:
            continue
        try:
            manejar(json.loads(linea))
        except ValueError:
            responder(None, error={"code": -32700, "message": "JSON invalido"})


if __name__ == "__main__":
    main()
