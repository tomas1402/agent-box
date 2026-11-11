// Package reloj es el estado del work item fuera del agente.
// El agente no escribe estos archivos. Publicar vuelve a correr los gates
// pineados en el alta y solo entonces arma el comentario y el PR.
package reloj

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

type Fase string

const (
	Admitido   Fase = "admitido"
	Trabajando Fase = "trabajando"
	Listo      Fase = "listo"
	Frenado    Fase = "frenado"
	Escalado   Fase = "escalado"
)

// Tope de publicar seguidos que fallan en el mismo gate.
const TopeFallos = 3

var idOK = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

type Gate struct {
	Nombre string `json:"nombre"`
	Cmd    string `json:"cmd"`
	Hard   bool   `json:"hard"`
	// TimeoutSec en cero usa GateTimeoutDefault.
	TimeoutSec int `json:"timeoutSec,omitempty"`
}

type Tracker struct {
	Provider string `json:"provider"`
	Repo     string `json:"repo"`
	OrgURL   string `json:"orgUrl,omitempty"`
	Project  string `json:"project,omitempty"`
}

// WI es el reloj de un work item. Gates, e2e y baseline se copian en el alta
// y publicar no vuelve a leer el worktree: el agente puede editar esos archivos.
type WI struct {
	ID       string `json:"id"`
	Fase     Fase   `json:"fase"`
	Worktree string `json:"worktree"`
	Gates    []Gate `json:"gates"`
	E2E      string `json:"e2e,omitempty"`
	// Setup son los comandos que dejan la caja lista (npm ci). Los corre preparar.
	Setup []string `json:"setup,omitempty"`
	// Reglas del harness y la revisión vigente, si la hay.
	Reglas    Reglas     `json:"reglas,omitempty"`
	Veredicto *Veredicto `json:"veredicto,omitempty"`
	// Titulo es el del work item. Con él se titula el PR.
	Titulo string `json:"titulo,omitempty"`
	// Rama es la rama real del worktree, leída al publicar. El PR sale de ahí.
	Rama           string         `json:"rama,omitempty"`
	PRTarget       string         `json:"prTarget,omitempty"`
	BranchPrefix   string         `json:"branchPrefix,omitempty"`
	PRTitlePrefix  string         `json:"prTitlePrefix,omitempty"`
	Tracker        Tracker        `json:"tracker"`
	Baseline       map[string]int `json:"baseline,omitempty"`
	Viajes         []ViajePin     `json:"viajes,omitempty"`
	Huecos         []string       `json:"huecos,omitempty"`
	Esperando      []string       `json:"esperando,omitempty"`
	FallosSeguidos int            `json:"fallosSeguidos"`
	UltimoGate     string         `json:"ultimoGate,omitempty"`
	UltimoPaso     string         `json:"ultimoPaso,omitempty"`
	Actividad      *Actividad     `json:"actividad,omitempty"`
	Corrida        *Corrida       `json:"corrida,omitempty"`
	Eventos        []Evento       `json:"eventos,omitempty"`
	Actualizado    string         `json:"actualizado"`
}

type Store struct {
	Dir string
}

func ValidarID(id string) error {
	if !idOK.MatchString(id) {
		return &Error{Codigo: "wi_invalido", Mensaje: "el id solo admite letras, numeros, punto, _ y -"}
	}
	return nil
}

func (s Store) path(id string) (string, error) {
	if err := ValidarID(id); err != nil {
		return "", err
	}
	return filepath.Join(s.Dir, id+".json"), nil
}

func (s Store) Load(id string) (WI, error) {
	p, err := s.path(id)
	if err != nil {
		return WI{}, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return WI{}, &Error{Codigo: "wi_no_admitido", Mensaje: "no hay alta para este WI"}
		}
		return WI{}, err
	}
	var w WI
	if err := json.Unmarshal(b, &w); err != nil {
		return WI{}, err
	}
	return w, nil
}

func (s Store) Save(w WI) error {
	if err := ValidarID(w.ID); err != nil {
		return err
	}
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return err
	}
	p, err := s.path(w.ID)
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(w, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func anotarFallo(w *WI, gate string, ahora time.Time) {
	if w.UltimoGate == gate {
		w.FallosSeguidos++
	} else {
		w.UltimoGate = gate
		w.FallosSeguidos = 1
	}
	if w.FallosSeguidos >= TopeFallos {
		w.ponerFase(Escalado, ahora, gate)
	} else {
		w.ponerFase(Frenado, ahora, gate)
	}
}

func marcarListo(w *WI, ahora time.Time) {
	w.ponerFase(Listo, ahora, "")
	w.FallosSeguidos = 0
	w.UltimoGate = ""
}

// Destrabar lo pide una persona. Baja el contador para que publicar vuelva a correr.
func Destrabar(s Store, id string, ahora time.Time) (WI, error) {
	w, err := s.Load(id)
	if err != nil {
		return WI{}, err
	}
	if w.Fase != Escalado && w.Fase != Frenado {
		return WI{}, &Error{Codigo: "no_frenado", Mensaje: "solo se destraba un WI frenado o escalado"}
	}
	w.evento(ahora, "fase", "destrabado", string(w.Fase))
	w.Fase = Frenado
	w.FallosSeguidos = 0
	w.UltimoGate = ""
	w.Actualizado = ahora.Format(time.RFC3339)
	if err := s.Save(w); err != nil {
		return WI{}, err
	}
	return w, nil
}

// Reabrir saca un WI de listo. Sirve cuando el PR se abandonó o cuando el listo
// lo dejó un ensayo de una versión vieja. Lo pide una persona.
func Reabrir(s Store, id string, ahora time.Time) (WI, error) {
	w, err := s.Load(id)
	if err != nil {
		return WI{}, err
	}
	if w.Fase != Listo {
		return WI{}, &Error{Codigo: "no_listo", Mensaje: "solo se reabre un WI listo"}
	}
	w.ponerFase(Trabajando, ahora, "reabierto")
	if err := s.Save(w); err != nil {
		return WI{}, err
	}
	return w, nil
}

type Error struct {
	Codigo  string
	Mensaje string
}

func (e *Error) Error() string {
	if e.Mensaje == "" {
		return e.Codigo
	}
	return e.Codigo + ": " + e.Mensaje
}
