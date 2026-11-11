# Imagen de ejemplo de una caja completa para agentes: Node, CLI de agentes, gh, Playwright con
# Chromium, binario de MongoDB para tests y .NET SDK. Adaptarla a los stacks de tus repos.
# Build en el NODO (no en una laptop con otra arquitectura):
#   docker build --build-arg AGENT_UID=<uid de agent en el host> -t orca-workspace-agent:<tag> image/
# Cada workspace = un contenedor de esta imagen, con sshd, al que Orca dial por SSH.
FROM ubuntu:24.04

ENV DEBIAN_FRONTEND=noninteractive \
    LANG=C.UTF-8 \
    ORCA_AGENT_USER=agent

RUN apt-get update && apt-get install -y --no-install-recommends \
      openssh-server ca-certificates curl git gnupg jq ripgrep less \
      build-essential python3 python3-pip python3-venv unzip tini sudo \
    && rm -rf /var/lib/apt/lists/*

# Node 22 (requisito de los CLI de agentes)
RUN curl -fsSL https://deb.nodesource.com/setup_22.x | bash - \
    && apt-get install -y --no-install-recommends nodejs \
    && rm -rf /var/lib/apt/lists/* \
    && corepack enable

# CLI de agentes, con version fija (fijar la misma que usa el operador en su maquina).
# Para subir de version: cambiar el ARG y rebuildear. DISABLE_AUTOUPDATER evita que claude
# intente actualizarse solo (npm global es de root; el agente no puede y quedaria a medias).
ARG CLAUDE_CODE_VERSION=2.1.300
ARG CODEX_VERSION=0.157.1
ENV DISABLE_AUTOUPDATER=1
RUN npm install -g @anthropic-ai/claude-code@${CLAUDE_CODE_VERSION} @openai/codex@${CODEX_VERSION} \
    && npm cache clean --force

# gh CLI (para clonar repos privados y abrir PRs desde el agente)
RUN curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg \
      -o /usr/share/keyrings/githubcli-archive-keyring.gpg \
    && echo "deb [signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" \
      > /etc/apt/sources.list.d/github-cli.list \
    && apt-get update && apt-get install -y --no-install-recommends gh \
    && rm -rf /var/lib/apt/lists/*

# Usuario no-root para el agente, con sudo sin password (el agente instala deps de proyecto).
# El uid DEBE coincidir con el del usuario agent del host (AGENT_UID): si no, el bind-mount
# del workspace no es escribible desde ninguno de los dos lados. Ubuntu 24.04 ya trae un
# usuario 'ubuntu' ocupando el 1000, asi que primero hay que correrlo. Si el usuario admin del
# host ya ocupa el 1000, usar el uid que le toque a agent (id -u agent).
ARG AGENT_UID=1000
RUN usermod -u 1002 ubuntu && groupmod -g 1002 ubuntu \
    && useradd -m -s /bin/bash -u "$AGENT_UID" -U "$ORCA_AGENT_USER" \
    && echo "$ORCA_AGENT_USER ALL=(ALL) NOPASSWD:ALL" > /etc/sudoers.d/90-agent \
    && mkdir -p /home/$ORCA_AGENT_USER/.ssh \
    && chown -R $ORCA_AGENT_USER:$ORCA_AGENT_USER /home/$ORCA_AGENT_USER/.ssh \
    && chmod 700 /home/$ORCA_AGENT_USER/.ssh

# Playwright + Chromium para journeys e2e. Navegadores en /ms-playwright (del agente, asi un
# @playwright/test local de otra version puede bajar el suyo ahi). NODE_PATH deja que un
# script suelto haga require('playwright') contra el global. Bajo gVisor Chromium corre con
# el sandbox propio de Chromium.
ARG PLAYWRIGHT_VERSION=1.64.0
ENV PLAYWRIGHT_BROWSERS_PATH=/ms-playwright \
    NODE_PATH=/usr/lib/node_modules
RUN npm install -g playwright@${PLAYWRIGHT_VERSION} \
    && playwright install --with-deps chromium \
    && chown -R "$ORCA_AGENT_USER:$ORCA_AGENT_USER" /ms-playwright \
    && rm -rf /var/lib/apt/lists/* && npm cache clean --force

# mongodb-memory-server: binario de Mongo pre-bajado (usar la misma version de mms que el lockfile
# de tus repos) a un dir compartido, asi los tests no lo bajan en cada caja nueva.
# Si otro repo pide otra version, la baja ahi mismo (dir del agente).
ARG MMS_VERSION=11.1.0
ENV MONGOMS_DOWNLOAD_DIR=/opt/mongodb-binaries
RUN mkdir -p /opt/mongodb-binaries /tmp/mms \
    && cd /tmp/mms && npm init -y >/dev/null \
    && npm install mongodb-memory-server@${MMS_VERSION} \
    && ls -la /opt/mongodb-binaries \
    && chown -R "$ORCA_AGENT_USER:$ORCA_AGENT_USER" /opt/mongodb-binaries \
    && cd / && rm -rf /tmp/mms && npm cache clean --force

# .NET SDK (ejemplo para repos ASP.NET Core 8; ajustar a la version de global.json de tus repos).
# Instalado con dotnet-install.sh en /usr/share/dotnet. Para subir de version: cambiar el ARG.
ARG DOTNET_SDK_VERSION=8.0.416
ENV DOTNET_ROOT=/usr/share/dotnet \
    DOTNET_CLI_TELEMETRY_OPTOUT=1 \
    DOTNET_NOLOGO=1 \
    PATH=/usr/share/dotnet:$PATH
RUN apt-get update && apt-get install -y --no-install-recommends libicu74 \
    && rm -rf /var/lib/apt/lists/* \
    && curl -fsSL https://dot.net/v1/dotnet-install.sh -o /tmp/dotnet-install.sh \
    && bash /tmp/dotnet-install.sh --version "${DOTNET_SDK_VERSION}" --install-dir /usr/share/dotnet \
    && rm /tmp/dotnet-install.sh \
    && ln -sf /usr/share/dotnet/dotnet /usr/local/bin/dotnet \
    && dotnet --list-sdks

# sshd: solo key, sin root, sin password
RUN mkdir -p /run/sshd \
    && printf '%s\n' \
      'PermitRootLogin no' \
      'PasswordAuthentication no' \
      'ChallengeResponseAuthentication no' \
      'KbdInteractiveAuthentication no' \
      'UsePAM yes' \
      'AcceptEnv LANG LC_* GH_TOKEN GITHUB_TOKEN' \
      'ClientAliveInterval 30' \
      'ClientAliveCountMax 120' \
      > /etc/ssh/sshd_config.d/10-orca.conf

# Host keys BAKEADAS en build: todos los contenedores presentan la misma host key,
# asi known_hosts no se rompe cuando el puerto publicado rota entre workspaces.
RUN ssh-keygen -A

COPY entrypoint.sh /usr/local/bin/orca-docker-ssh-entrypoint
RUN chmod +x /usr/local/bin/orca-docker-ssh-entrypoint

EXPOSE 22
ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/orca-docker-ssh-entrypoint"]
