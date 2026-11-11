package reloj

import (
	"path/filepath"
	"strings"
)

// Spec es la caja que boxd crearía. Corte A no llama a docker.
type Spec struct {
	Nombre       string
	Runtime      string
	Init         bool
	MemoryMaxMB  int
	MemoryHighMB int
	Worktree     string
	Montajes     []string
	Env          []string
	// Lo que sigue lo pone el nodo (box.env). Vacío no se pasa a docker.
	Imagen string
	Red    string
	CPUs   string
	Shm    string
	Pids   int
}

var envProhibido = []string{
	"GH_TOKEN",
	"GITHUB_TOKEN",
	"AZURE_DEVOPS_EXT_PAT",
	"AZURE_DEVOPS_PAT",
	"AZURE_TOKEN",
	"SYSTEM_ACCESSTOKEN",
}

func PlanCaja(id, worktree string, p Perfil) (Spec, error) {
	if err := ValidarID(id); err != nil {
		return Spec{}, err
	}
	if err := p.validar(); err != nil {
		return Spec{}, err
	}
	if worktree == "" || !filepath.IsAbs(worktree) {
		return Spec{}, &Error{Codigo: "worktree", Mensaje: "el worktree tiene que ser un path absoluto"}
	}
	s := Spec{
		Nombre:       "box-" + id,
		Runtime:      "runsc",
		Init:         true,
		MemoryMaxMB:  p.EdicionMaxMB,
		MemoryHighMB: p.EdicionHighMB,
		Worktree:     worktree,
		Montajes:     []string{worktree + ":" + worktree},
	}
	if err := SpecLimpia(s); err != nil {
		return Spec{}, err
	}
	return s, nil
}

// SpecLimpia rechaza socket de docker, el env del supervisor y tokens.
func SpecLimpia(s Spec) error {
	for _, m := range s.Montajes {
		bajo := strings.ToLower(m)
		if strings.Contains(bajo, "docker.sock") || strings.Contains(bajo, "supervisor.env") {
			return &Error{Codigo: "caja_sucia", Mensaje: "montaje prohibido: " + m}
		}
	}
	for _, e := range s.Env {
		clave := e
		if i := strings.IndexByte(e, '='); i >= 0 {
			clave = e[:i]
		}
		for _, p := range envProhibido {
			if clave == p {
				return &Error{Codigo: "credencial_en_la_caja", Mensaje: clave}
			}
		}
	}
	if s.MemoryHighMB <= 0 || s.MemoryHighMB >= s.MemoryMaxMB {
		return &Error{Codigo: "perfil", Mensaje: "memory.high tiene que ser menor que memory.max"}
	}
	return nil
}

// Sonda lee lo que la caja tiene montado y en el entorno.
type Sonda interface {
	Env(id string) ([]string, error)
	Montajes(id string) ([]string, error)
}

func sondaLimpia(s Sonda, id string) error {
	if s == nil {
		return &Error{Codigo: "sonda_ausente", Mensaje: "sin sonda no se corre publicar"}
	}
	env, err := s.Env(id)
	if err != nil {
		return err
	}
	mont, err := s.Montajes(id)
	if err != nil {
		return err
	}
	return SpecLimpia(Spec{MemoryHighMB: 1, MemoryMaxMB: 2, Env: env, Montajes: mont})
}

// SondaVacia es la del corte A: no hay contenedor todavía, y no se inventa un token.
type SondaVacia struct{}

func (SondaVacia) Env(string) ([]string, error)      { return nil, nil }
func (SondaVacia) Montajes(string) ([]string, error) { return nil, nil }
