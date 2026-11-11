package reloj

import (
	"encoding/json"
	"os"
)

// configArchivo es el harness.config.json del repo. Se lee una vez, en el alta.
type configArchivo struct {
	Gates []struct {
		Name string `json:"name"`
		Cmd  string `json:"cmd"`
		Hard bool   `json:"hard"`
		// timeoutSec es opcional. Sin él vale GateTimeoutDefault.
		TimeoutSec int `json:"timeoutSec"`
	} `json:"gates"`
	Setup  []string `json:"setup"`
	Reglas Reglas   `json:"reglas"`
	E2E    *struct {
		Artifact string `json:"artifact"`
	} `json:"e2e"`
	PRTarget      string  `json:"prTarget"`
	BranchPrefix  string  `json:"branchPrefix"`
	PRTitlePrefix string  `json:"prTitlePrefix"`
	Tracker       Tracker `json:"tracker"`
}

func LeerConfig(path string) (WI, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return WI{}, err
	}
	if len(b) > 0 && b[0] != '{' {
		return WI{}, &Error{Codigo: "config_no_json", Mensaje: "harness.config.json tiene que ser JSON, sin comentarios"}
	}
	var c configArchivo
	if err := json.Unmarshal(b, &c); err != nil {
		return WI{}, &Error{Codigo: "config_no_json", Mensaje: err.Error()}
	}
	w := WI{
		PRTarget:      c.PRTarget,
		BranchPrefix:  c.BranchPrefix,
		PRTitlePrefix: c.PRTitlePrefix,
		Tracker:       c.Tracker,
		Setup:         c.Setup,
		Reglas:        c.Reglas,
	}
	if c.E2E != nil {
		w.E2E = c.E2E.Artifact
	}
	for _, g := range c.Gates {
		if g.Name == "" || g.Cmd == "" {
			return WI{}, &Error{Codigo: "gate_incompleto", Mensaje: "cada gate necesita name y cmd"}
		}
		w.Gates = append(w.Gates, Gate{Nombre: g.Name, Cmd: g.Cmd, Hard: g.Hard, TimeoutSec: g.TimeoutSec})
	}
	return w, nil
}

func LeerBaseline(path string) (map[string]int, error) {
	if path == "" {
		return map[string]int{}, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]int{}, nil
		}
		return nil, err
	}
	var m map[string]int
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, &Error{Codigo: "baseline_no_json", Mensaje: err.Error()}
	}
	if m == nil {
		m = map[string]int{}
	}
	return m, nil
}

func gatesDuros(gs []Gate) []Gate {
	var out []Gate
	for _, g := range gs {
		if g.Hard {
			out = append(out, g)
		}
	}
	return out
}

// preexisting: el mismo exit distinto de cero que ya tenía la base.
// Otro exit, aunque también falle, no es el mismo fallo.
func preexisting(base map[string]int, nombre string, exit int) bool {
	if exit == 0 || base == nil {
		return false
	}
	prev, ok := base[nombre]
	return ok && prev != 0 && prev == exit
}
