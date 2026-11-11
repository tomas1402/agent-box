package reloj

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// SalidaAz ejecuta de verdad el comando del proveedor (az, gh), con las credenciales del supervisor. Ese
// archivo vive en el host (600, root) y nunca se monta en la caja.
type SalidaAz struct {
	EnvFile string
	// Exec se reemplaza en los tests. Devuelve stdout.
	Exec func(ctx context.Context, env []string, argv []string) (string, error)
}

// LeerEnvFile lee líneas CLAVE=valor. Ignora vacías y comentarios.
func LeerEnvFile(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, &Error{Codigo: "credencial_ausente", Mensaje: "no se pudo leer el env del supervisor"}
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		if l == "" || strings.HasPrefix(l, "#") || !strings.Contains(l, "=") {
			continue
		}
		out = append(out, strings.TrimPrefix(l, "export "))
	}
	return out, sc.Err()
}

func (s SalidaAz) correr(ctx context.Context, argv []string) (string, error) {
	env, err := LeerEnvFile(s.EnvFile)
	if err != nil {
		return "", err
	}
	if s.Exec != nil {
		return s.Exec(ctx, env, argv)
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	c := exec.CommandContext(ctx, argv[0], argv[1:]...)
	c.Env = append(os.Environ(), env...)
	out, err := c.Output()
	if err != nil {
		// stderr de az puede traer la URL con el token: no se propaga.
		return "", &Error{Codigo: "cli", Mensaje: argv[0] + " " + argv[1] + " " + argv[2] + " falló"}
	}
	return string(out), nil
}

func (s SalidaAz) Comentar(ctx context.Context, argv []string) error {
	_, err := s.correr(ctx, argv)
	return err
}

// AbrirPR devuelve el stdout del comando. Cada proveedor sabe sacar de ahí la URL.
func (s SalidaAz) AbrirPR(ctx context.Context, argv []string) (string, error) {
	return s.correr(ctx, argv)
}

func itoa(n int) string { return strconv.Itoa(n) }
