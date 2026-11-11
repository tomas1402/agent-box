---
description: Estado y capacidad de todos los nodos de la box de agentes
---
Corre `boxctl --json nodos` (o la tool MCP `box_nodos`) y responde en una tabla: nodo, alcanzable,
memoria de la caja usada/limite, agentes, workersCaben, suitesCaben, deriva (cantidad). Primera
linea: si hay lugar para un worker mas y en que nodo. Si algun nodo da error, codigo y mensaje.
