package reloj

import (
	"context"
	"errors"
	"os/exec"
	"syscall"
	"time"
)

// Tope de un gate si el config no dice otro. Una suite colgada no puede
// dejar la caja con el burst de RAM tomado para siempre.
const GateTimeoutDefault = 30 * time.Minute

// Bash corre el cmd del gate en el worktree. El cmd sale del pin del alta,
// no de lo que escriba el agente en el momento de publicar.
type Bash struct{}

func (Bash) Run(ctx context.Context, dir, _nombre, cmd string) (int, string, error) {
	c := exec.CommandContext(ctx, "bash", "-lc", cmd)
	c.Dir = dir
	// Grupo propio: al vencer el plazo se mata el árbol entero (jest, chromium),
	// no solo el bash que lo lanzó.
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Cancel = func() error { return syscall.Kill(-c.Process.Pid, syscall.SIGKILL) }
	c.WaitDelay = 5 * time.Second
	out, err := c.CombinedOutput()
	cola := string(out)
	if len(cola) > 2048 {
		cola = cola[len(cola)-2048:]
	}
	if err == nil {
		return 0, cola, nil
	}
	if ctx.Err() == context.DeadlineExceeded {
		return 124, cola + "\n[boxd] el gate pasó su plazo y se cortó", nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), cola, nil
	}
	return -1, cola, err
}
