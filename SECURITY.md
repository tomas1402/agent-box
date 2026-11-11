# Seguridad

## Qué protege agent-box y qué no

- La caja de un work item nace **sin credenciales**: `boxd` no le pasa archivos de entorno y se
  niega a publicar si encuentra un token en su entorno o el socket de Docker montado.
- Los gates que deciden la salida los corre `boxd` desde el host, con los comandos que copió al
  admitir el work item. El agente no puede cambiarlos.
- El aislamiento es el del runtime que elijas: con `runc` es el de Docker; con `runsc` (gVisor) es
  más fuerte. agent-box no agrega aislamiento propio.
- El worktree y el `.git` del clon se montan con escritura. Un agente puede reescribir la historia
  local de ese repo.
- El firewall de salida (`host/firewall`) es opcional y filtra por lista de dominios. No inspecciona
  contenido.

No uses una caja como única barrera frente a código que no confiás.

## Reportar un problema

Usá "Report a vulnerability" en la pestaña Security del repositorio en GitHub, que abre un aviso
privado. No abras un issue público hasta tener respuesta. Incluí los pasos para reproducirlo.
