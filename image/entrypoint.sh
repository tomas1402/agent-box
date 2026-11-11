#!/usr/bin/env bash
# Entrypoint del contenedor-workspace: instala la pubkey que manda `create` y levanta sshd.
# Lo usa image/Dockerfile (COPY + ENTRYPOINT); si tu imagen no corre sshd, no hace falta.
set -euo pipefail

agent_user="${ORCA_AGENT_USER:-agent}"
agent_home="$(getent passwd "$agent_user" | cut -d: -f6)"

if [ -z "${ORCA_SSH_PUBLIC_KEY:-}" ]; then
  echo "ORCA_SSH_PUBLIC_KEY no fue provista; sshd quedaria inaccesible" >&2
  exit 1
fi

install -d -m 700 -o "$agent_user" -g "$agent_user" "$agent_home/.ssh"
printf '%s\n' "$ORCA_SSH_PUBLIC_KEY" > "$agent_home/.ssh/authorized_keys"
chown "$agent_user:$agent_user" "$agent_home/.ssh/authorized_keys"
chmod 600 "$agent_home/.ssh/authorized_keys"

# Solo si faltan (la imagen base ya las trae bakeadas)
ssh-keygen -A >/dev/null 2>&1 || true

mkdir -p /run/sshd
exec /usr/sbin/sshd -D -e
