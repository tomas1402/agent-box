package reloj

import "context"

// Registro anota los argv que se le habrían pasado a az. No ejecuta nada.
type Registro struct {
	Llamadas [][]string
	FallarEn string // "wi" o "pr"
}

func (r *Registro) Comentar(_ context.Context, argv []string) error {
	r.Llamadas = append(r.Llamadas, append([]string(nil), argv...))
	if r.FallarEn == "wi" {
		return &Error{Codigo: "wi", Mensaje: "comentario rechazado"}
	}
	return nil
}

func (r *Registro) AbrirPR(_ context.Context, argv []string) (string, error) {
	r.Llamadas = append(r.Llamadas, append([]string(nil), argv...))
	if r.FallarEn == "pr" {
		return "", &Error{Codigo: "pr", Mensaje: "pr rechazado"}
	}
	return "https://dev.azure.com/ejemplo/pr/1", nil
}
