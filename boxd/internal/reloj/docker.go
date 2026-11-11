package reloj

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Etiqueta que marca una caja de WI. Con ella se listan las vivas para el
// presupuesto de RAM, sin que nadie tenga que pasarlas a mano.
const EtiquetaWI = "agent-box.wi"

// Docker es el backend real: crea la caja, le mueve el techo de RAM, la
// inspecciona y corre los gates adentro. Bin se puede cambiar en los tests.
type Docker struct {
	Bin string
}

func (d Docker) bin() string {
	if d.Bin != "" {
		return d.Bin
	}
	return "docker"
}

func (d Docker) salida(ctx context.Context, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, d.bin(), args...).CombinedOutput()
	if err != nil {
		return "", &Error{Codigo: "docker", Mensaje: strings.TrimSpace(d.bin() + " " + args[0] + ": " + ultimaLinea(string(out)))}
	}
	return strings.TrimSpace(string(out)), nil
}

func ultimaLinea(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}

// ArgsDockerRun arma el docker run de la caja de un WI. No hay --env-file:
// lo que entra al entorno es solo lo que diga la Spec, y SpecLimpia ya rechazó
// tokens y montajes del host.
func ArgsDockerRun(s Spec) []string {
	m := fmt.Sprintf("%dm", s.MemoryMaxMB)
	args := []string{"run", "-d", "--name", s.Nombre,
		"--label", EtiquetaWI + "=" + strings.TrimPrefix(s.Nombre, "box-"),
		"--memory", m, "--memory-swap", m}
	if s.Runtime != "" {
		args = append(args, "--runtime", s.Runtime)
	}
	if s.Init {
		args = append(args, "--init")
	}
	if s.CPUs != "" {
		args = append(args, "--cpus", s.CPUs)
	}
	if s.Pids > 0 {
		args = append(args, "--pids-limit", strconv.Itoa(s.Pids))
	}
	if s.Shm != "" {
		args = append(args, "--shm-size", s.Shm)
	}
	if s.Red != "" {
		args = append(args, "--network", s.Red)
	}
	for _, mnt := range s.Montajes {
		args = append(args, "-v", mnt)
	}
	for _, e := range s.Env {
		args = append(args, "-e", e)
	}
	args = append(args, "-w", s.Worktree, "--entrypoint", "sleep", s.Imagen, "infinity")
	return args
}

// Crear levanta la caja. Si ya existe con ese nombre la arranca y no la recrea:
// recrear tiraría lo que el agente instaló adentro.
func (d Docker) Crear(ctx context.Context, s Spec) error {
	if err := SpecLimpia(s); err != nil {
		return err
	}
	if s.Imagen == "" {
		return &Error{Codigo: "imagen", Mensaje: "falta la imagen de la caja"}
	}
	estado, _ := d.salida(ctx, "inspect", "-f", "{{.State.Status}}", s.Nombre)
	switch estado {
	case "running":
		return nil
	case "exited", "created", "paused":
		_, err := d.salida(ctx, "start", s.Nombre)
		return err
	}
	_, err := d.salida(ctx, ArgsDockerRun(s)...)
	return err
}

// Baja borra la caja. El worktree vive en el host y no se toca.
func (d Docker) Baja(ctx context.Context, id string) error {
	if err := ValidarID(id); err != nil {
		return err
	}
	_, err := d.salida(ctx, "rm", "-f", "box-"+id)
	return err
}

// Aplicar mueve el techo duro. Docker no escribe memory.high: devuelve falso.
func (d Docker) Aplicar(id string, maxMB, _ int) (bool, error) {
	args := ArgsDockerUpdate("box-"+id, maxMB)
	_, err := d.salida(context.Background(), args[1:]...)
	return false, err
}

type inspeccion struct {
	Config struct {
		Env []string `json:"Env"`
	} `json:"Config"`
	Mounts []struct {
		Source      string `json:"Source"`
		Destination string `json:"Destination"`
	} `json:"Mounts"`
	HostConfig struct {
		Memory int64 `json:"Memory"`
	} `json:"HostConfig"`
}

func (d Docker) inspeccionar(id string) (inspeccion, error) {
	out, err := d.salida(context.Background(), "inspect", "box-"+id)
	if err != nil {
		return inspeccion{}, err
	}
	var lista []inspeccion
	if err := json.Unmarshal([]byte(out), &lista); err != nil || len(lista) != 1 {
		return inspeccion{}, &Error{Codigo: "docker", Mensaje: "inspect no devolvió una caja"}
	}
	return lista[0], nil
}

// Env y Montajes son la sonda: lo que la caja tiene de verdad, no lo que se pidió.
func (d Docker) Env(id string) ([]string, error) {
	i, err := d.inspeccionar(id)
	return i.Config.Env, err
}

func (d Docker) Montajes(id string) ([]string, error) {
	i, err := d.inspeccionar(id)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, m := range i.Mounts {
		out = append(out, m.Source+":"+m.Destination)
	}
	return out, nil
}

// Vivas lista las cajas de WI que están corriendo, con su techo actual.
func (d Docker) Vivas(ctx context.Context) ([]CajaViva, error) {
	out, err := d.salida(ctx, "ps", "-q", "--filter", "label="+EtiquetaWI)
	if err != nil || out == "" {
		return nil, err
	}
	ids := strings.Fields(out)
	fmtStr := "{{index .Config.Labels \"" + EtiquetaWI + "\"}} {{.HostConfig.Memory}}"
	out, err = d.salida(ctx, append([]string{"inspect", "-f", fmtStr}, ids...)...)
	if err != nil {
		return nil, err
	}
	var vivas []CajaViva
	for _, l := range strings.Split(out, "\n") {
		c := strings.Fields(l)
		if len(c) != 2 {
			continue
		}
		b, _ := strconv.ParseInt(c[1], 10, 64)
		vivas = append(vivas, CajaViva{ID: c[0], MaxMB: int(b / (1024 * 1024))})
	}
	return vivas, nil
}

// EnCaja corre el gate adentro de la caja del WI, como el usuario que se
// indique. Es el runner de verdad: el burst de RAM es para este proceso.
type EnCaja struct {
	Docker  Docker
	ID      string
	Usuario string
}

func (e EnCaja) Run(ctx context.Context, dir, _ string, cmd string) (int, string, error) {
	args := []string{"exec", "-w", dir}
	if e.Usuario != "" {
		args = append(args, "-u", e.Usuario)
	}
	args = append(args, "box-"+e.ID, "bash", "-lc", cmd)
	c := exec.CommandContext(ctx, e.Docker.bin(), args...)
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
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
		// Matar el docker exec no mata lo que quedó adentro: se corta por nombre de comando.
		_ = exec.Command(e.Docker.bin(), "exec", "box-"+e.ID, "pkill", "-KILL", "-f", cmd).Run()
		return 124, cola + "\n[boxd] el gate pasó su plazo y se cortó", nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), cola, nil
	}
	return -1, cola, err
}

// Metrica es el consumo de una caja ahora mismo.
type Metrica struct {
	Viva    bool    `json:"viva"`
	MemMB   int     `json:"memMb"`
	TechoMB int     `json:"techoMb"`
	CPUPct  float64 `json:"cpuPct"`
}

func aMB(s string) int {
	s = strings.TrimSpace(s)
	unidades := []struct {
		suf string
		mul float64
	}{{"GiB", 1024}, {"MiB", 1}, {"KiB", 1.0 / 1024}, {"GB", 953.67}, {"MB", 0.9537}, {"kB", 0.00095}, {"B", 1.0 / (1024 * 1024)}}
	for _, u := range unidades {
		if strings.HasSuffix(s, u.suf) {
			v, err := strconv.ParseFloat(strings.TrimSuffix(s, u.suf), 64)
			if err != nil {
				return 0
			}
			return int(v*u.mul + 0.5)
		}
	}
	return 0
}

// Metricas lee de un solo docker stats el consumo de todas las cajas de WI vivas.
func (d Docker) Metricas(ctx context.Context) map[string]Metrica {
	out := map[string]Metrica{}
	ids, err := d.salida(ctx, "ps", "-q", "--filter", "label="+EtiquetaWI)
	if err != nil || ids == "" {
		return out
	}
	args := append([]string{"stats", "--no-stream", "--format", "{{.Name}}|{{.MemUsage}}|{{.CPUPerc}}"}, strings.Fields(ids)...)
	txt, err := d.salida(ctx, args...)
	if err != nil {
		return out
	}
	for _, l := range strings.Split(txt, "\n") {
		c := strings.Split(l, "|")
		if len(c) != 3 || !strings.HasPrefix(c[0], "box-") {
			continue
		}
		uso, techo, _ := strings.Cut(c[1], "/")
		cpu, _ := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(c[2]), "%"), 64)
		out[strings.TrimPrefix(c[0], "box-")] = Metrica{Viva: true, MemMB: aMB(uso), TechoMB: aMB(techo), CPUPct: cpu}
	}
	return out
}
