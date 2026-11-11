# Instalar agent-box en un nodo Linux

Un nodo es una maquina Linux donde corre la caja de agentes: un contenedor con techo de memoria,
CPU y procesos, al que caen las sesiones SSH del usuario `agent`. `install.sh` lo deja instalado
de forma idempotente y con backup. Este documento sirve para el primer nodo y para actualizarlo.

Convencion: `<nodo>` es el nombre del nodo (`[a-z0-9-]+`, por ejemplo `nodo-a`). Tiene que existir
`config/box.env.<nodo>.example` en el repo; los ejemplos incluidos son `nodo-a` y `nodo-b`. Para
un nodo nuevo, copiar uno de los dos con el nombre elegido y ajustar los limites.

## Requisitos

- Ubuntu 24.04 (otra version solo da un aviso), con acceso root (`sudo`).
- Paquetes: `docker`, `python3`, `jq`, `curl`, `ipset`, `iptables`, `getent`, `sha256sum`,
  `systemd-run` y `rsync`. El modo informe avisa cuales faltan.
- Un usuario `agent` en el host (su uid tiene que coincidir con `AGENT_UID` al buildear la imagen).
- Si `RUNTIME=runsc` en `box.env`: gVisor registrado en docker (`runsc install` y reiniciar
  docker). El instalador avisa si no esta.
- La imagen de la caja (`IMAGE` en `box.env`) construida en el nodo. `install.sh` no la buildea:
  `docker build --build-arg AGENT_UID=$(id -u agent) -t <IMAGE> image/` (ver `image/Dockerfile`,
  que es un ejemplo de caja completa para adaptar).
- Para operarlo a distancia: un alias ssh del usuario admin del nodo (no el de `agent`), por
  ejemplo `nodo-a-admin`, y `config/nodos.json.example` copiado a
  `~/.config/agent-box/nodos.json` en la maquina del operador.

## Pasos

1. **Copiar el repo al nodo** (sin git en el nodo), desde la maquina del operador:

   ```
   rsync -a --delete ./ nodo-a-admin:/tmp/agent-box/
   ```

2. **Modo informe** (no escribe nada). En el nodo:

   ```
   sudo /tmp/agent-box/install.sh --node <nodo>
   ```

   Imprime, por cada paso, que existe, que difiere y que "haria", mas los avisos (paquetes
   faltantes, runtime, red con otra subred). Revisarlo entero antes de seguir.

3. **Revisar los limites.** Si ya existe `/etc/agent-box/box.env`, el instalador no lo pisa (es
   del operador): muestra el diff contra el ejemplo. Si no existe, lo crea desde
   `config/box.env.<nodo>.example`. Ajustar `MEM_GB`, `CPUS`, `WORKERS_MAX`, `GIT_NAME` y
   `GIT_EMAIL` a la maquina antes de crear la caja. `box.env` es la unica fuente de limites.

4. **Aplicar.**

   ```
   sudo /tmp/agent-box/install.sh --node <nodo> --aplicar
   ```

   Cada paso se saltea si ya esta igual. Con `--recrear-caja` ademas recrea la caja, y solo si
   no hay agentes vivos (si los hay, `boxctl caja-reiniciar` rechaza con exit 6 y no toca nada).

5. **Secretos.** El valor entra por stdin, nunca por argumento. La caja los toma al proximo
   `boxctl caja-reiniciar`. Antes, crear el directorio:
   `sudo install -d -m 750 -o root -g agent /etc/orca-agent`. Despues:

   ```
   gh auth token | ssh nodo-a-admin sudo orca-secret-set GH_TOKEN
   ```

6. **Hook de cola de suites dentro de la caja** (opcional): activa `gate-lock.sh` y el hook
   `cola-suites.sh` para el usuario `agent` de la caja. Desde la maquina del operador:

   ```
   ssh nodo-a 'bash -s' < caja/instalar-en-caja.sh
   ```

   Solo afecta a sesiones de Claude Code que arranquen despues.

7. **Firewall de egress** (paso propio, no lo hace `install.sh`). Mejor sin agentes, porque corta
   lo que no este en `config/egress-allowlist.txt`:

   ```
   sudo boxctl firewall aplicar --deadman 600     # auto-rollback a los 10 minutos
   sudo boxctl firewall verificar
   sudo boxctl firewall confirmar                  # solo si verificar dio todo ok
   sudo touch /etc/agent-box/egress-habilitado
   sudo systemctl enable agent-box-egress.service  # reaplica al arrancar
   ```

   Se aplica en la cadena `DOCKER-USER`, solo al trafico que entra desde el bridge de la caja
   (`RED_BRIDGE`): no toca `INPUT` ni `OUTPUT` del host, asi que no deja a nadie afuera del nodo.
   Corta la LAN, `100.64.0.0/10` y link-local antes de aplicar la allowlist.

## Que deja instalado y donde

| Que | Donde |
|---|---|
| Limites del nodo (no se pisa si existe) | `/etc/agent-box/box.env` |
| CLI y shell de agentes | `/usr/local/bin/boxctl`, `orca-agent-shell`, `orca-secret-set` |
| Firewall de egress y su allowlist | `/usr/local/sbin/agent-box-egress`, `/etc/agent-box/egress-allowlist.txt` |
| Unidades systemd | `/etc/systemd/system/agent-box-*.service` y `.timer` |
| Bitacora de operaciones (JSON, una por linea) | `/var/log/agent-box.jsonl` |
| Red docker de la caja (subred y bridge fijos) | `RED`, `RED_SUBNET`, `RED_BRIDGE` de `box.env` |
| `~/.orca` persistente de la caja | `ORCA_STATE` (dueño `agent`, modo 700) |
| `~/.claude` persistente (si `CLAUDE_STATE` esta definido) | `CLAUDE_STATE` |
| Archivos para adentro de la caja | `$WORKSPACE/.agent-box/caja` |
| Backups de lo reemplazado | `/root/backups-agent-box/<fecha-hora>/` |

Timers habilitados: `agent-box-mantenimiento` (04:00, limpieza de imagenes y cache),
`agent-box-egress-refresh` (re-resuelve la allowlist), `agent-box-gc` (procesos, cada 10 min),
`agent-box-gc-disco` (04:30) y `agent-box-vigia` (memoria, cada 30 s).

`install.sh` **no** aplica el firewall, no toca secretos, no buildea la imagen, no toca la red
del host ni `INPUT`/`OUTPUT` de iptables, y no toca otras aplicaciones del host.

## GC y vigia

Los timers del GC arrancan en **dry-run**: con `GC_APLICAR=` vacio en `box.env` solo leen `ps`
y escriben en la bitacora. `boxctl gc` cubre procesos huerfanos sin padre `claude`, suites que
pasan `SUITE_MAX_MIN` o `SUITE_MAX_GB`, `/tmp`, caches de worktrees inactivos y worktrees
`seguro`. El vigia (`gc --alcance memoria`) alerta al `MEM_ALERTA_PCT` y corta la suite mas
grande al `MEM_CORTE_PCT`, porque bajo gVisor el OOM del cgroup mata la caja entera.

Para activarlo: leer la bitacora (`sudo boxctl logs -n 200`, entradas `gc-plan`) y, si lo que
mataria es correcto, poner `GC_APLICAR=--aplicar` en `/etc/agent-box/box.env`. Para volver a
dry-run, dejarlo vacio.

`boxctl salvar --aplicar` guarda un snapshot de cada worktree sucio en `refs/wip/<rama>/<ts>` y
lo empuja sin force; `caja-reiniciar` lo corre siempre antes y frena si falla.

## Verificar

```
sudo boxctl --json estado            # deriva == [] y capacidad del nodo
sudo /tmp/agent-box/install.sh --node <nodo>     # modo informe: todo "igual", 0 cambios
systemctl list-timers 'agent-box-*'
sudo boxctl firewall estado
```

Desde la maquina del operador, con `nodos.json` configurado: `boxctl --json --nodo <nodo> estado`
y `boxctl --json nodos`. Una suite lanzada con `gate-lock.sh test auto -- <comando>` adentro de la
caja debe esperar turno segun la RAM libre. Con el firewall activo, adentro de la caja debe
resolver y alcanzar `api.github.com` y no alcanzar un host fuera de la allowlist.

## Volver atras

- **Binarios, scripts y unidades**: restaurar desde `/root/backups-agent-box/<fecha-hora>/`
  (`sudo cp -a /root/backups-agent-box/<fecha-hora>/usr/local/bin/* /usr/local/bin/`, lo mismo
  para `/etc/systemd/system`) y `sudo systemctl daemon-reload`.
- **Timers**: `sudo systemctl disable --now agent-box-mantenimiento.timer
  agent-box-egress-refresh.timer agent-box-gc.timer agent-box-gc-disco.timer
  agent-box-vigia.timer`.
- **Firewall**: `sudo boxctl firewall quitar` (o esperar el auto-rollback si no se confirmo) y
  `sudo rm /etc/agent-box/egress-habilitado`.
- **Red de la caja**: `sudo docker network rm <RED>` (con la caja detenida).
- **Config**: `box.env` nunca se pisa; si se edito, se restaura a mano.
- **Caja**: `sudo boxctl caja-reiniciar` la recrea desde `box.env`; el workspace, `ORCA_STATE` y
  `CLAUDE_STATE` sobreviven. Si falla solo el DNS bajo gVisor, probar `RED=bridge` en
  `box.env` y recrear.
