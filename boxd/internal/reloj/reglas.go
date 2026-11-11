package reloj

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// Reglas son las condiciones de proceso que el harness puede exigir además de
// los gates. Se chequean antes de correr nada: son baratas y no gastan RAM.
type Reglas struct {
	// TestsConCodigo: una rama que toca código tiene que tocar también tests o e2e.
	TestsConCodigo bool `json:"testsConCodigo,omitempty"`
	// Veredicto: alguien que no es el autor aprobó exactamente este commit.
	Veredicto bool `json:"veredicto,omitempty"`
}

// Veredicto es la revisión de un commit. Lo anota quien revisa, desde el host;
// la caja no tiene cómo escribirlo. Vale para un solo commit: un push nuevo lo vence.
type Veredicto struct {
	SHA      string `json:"sha"`
	Aprobado bool   `json:"aprobado"`
	Quien    string `json:"quien"`
	Nota     string `json:"nota,omitempty"`
	TS       string `json:"ts"`
}

// Head devuelve el commit del worktree.
func (GitCLI) Head(ctx context.Context, worktree string) string {
	out, err := exec.CommandContext(ctx, "git", "-C", worktree, "rev-parse", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

type conHead interface {
	Head(context.Context, string) string
}

// AnotarVeredicto guarda la revisión del commit actual del worktree.
func AnotarVeredicto(ctx context.Context, st Store, g Git, id string, aprobado bool, quien, nota string, ahora time.Time) (WI, error) {
	if strings.TrimSpace(quien) == "" {
		return WI{}, &Error{Codigo: "veredicto_sin_autor", Mensaje: "decí quién revisó con --quien"}
	}
	soltar, ocupado, err := st.Trabar(id, true)
	if err != nil {
		return WI{}, err
	}
	_ = ocupado
	defer soltar()
	w, err := st.Load(id)
	if err != nil {
		return WI{}, err
	}
	h, ok := g.(conHead)
	sha := ""
	if ok {
		sha = h.Head(ctx, w.Worktree)
	}
	if sha == "" {
		return WI{}, &Error{Codigo: "git", Mensaje: "no se pudo leer el commit del worktree"}
	}
	w.Veredicto = &Veredicto{SHA: sha, Aprobado: aprobado, Quien: quien, Nota: nota, TS: ahora.Format(time.RFC3339)}
	valor := "aprobado"
	if !aprobado {
		valor = "rechazado"
	}
	w.evento(ahora, "veredicto", valor, quien)
	w.Actualizado = ahora.Format(time.RFC3339)
	return w, st.Save(w)
}

// chequearReglas devuelve el código de la primera regla que no se cumple, o vacío.
func chequearReglas(ctx context.Context, w WI, g Git, ahora time.Time) (codigo, detalle string) {
	if w.Reglas.TestsConCodigo && g != nil {
		if a, err := Observar(ctx, g, w, ahora); err == nil && a.Archivos[EtapaCodigo] > 0 && a.Archivos[EtapaTests] == 0 && a.Archivos[EtapaE2E] == 0 {
			return "sin_tests", "la rama cambia código y no toca ningún test ni e2e"
		}
	}
	if w.Reglas.Veredicto {
		v := w.Veredicto
		if v == nil {
			return "sin_veredicto", "falta la revisión de alguien que no sea el autor: boxd veredicto --wi " + w.ID + " --aprueba --quien NOMBRE"
		}
		if !v.Aprobado {
			return "veredicto_rechazado", "la revisión de " + v.Quien + " rechazó este cambio: " + v.Nota
		}
		if h, ok := g.(conHead); ok {
			if sha := h.Head(ctx, w.Worktree); sha != "" && sha != v.SHA {
				return "veredicto_viejo", "la revisión es de otro commit (" + corto(v.SHA) + "); el actual es " + corto(sha)
			}
		}
	}
	return "", ""
}

func corto(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}
