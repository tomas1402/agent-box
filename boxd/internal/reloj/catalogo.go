package reloj

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Viaje es una entrada de e2e/catalogo.json. El archivo vive en el repo.
type Viaje struct {
	ID        string   `json:"id"`
	AC        []string `json:"ac"`
	Modo      string   `json:"modo"`
	Cmd       string   `json:"cmd"`
	Artefacto string   `json:"artefacto"`
	Reemplaza string   `json:"reemplaza,omitempty"`
}

// ViajePin es la copia que el alta guarda en el host. Publicar no relee el catálogo
// para decidir qué viajes viejos corre.
type ViajePin struct {
	ID        string   `json:"id"`
	AC        []string `json:"ac"`
	Cmd       string   `json:"cmd"`
	Artefacto string   `json:"artefacto"`
}

type Catalogo struct {
	Viajes []Viaje `json:"viajes"`
}

func LeerCatalogo(path string) (Catalogo, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Catalogo{}, err
	}
	var c Catalogo
	if err := json.Unmarshal(b, &c); err != nil {
		return Catalogo{}, &Error{Codigo: "catalogo_no_json", Mensaje: err.Error()}
	}
	return c, nil
}

// Matchear parte los criterios. Headless se pinea. Browse no cubre el gate
// desatendido: queda en esperando. Lo que nadie cubre es un hueco.
func Matchear(acs []string, viajes []Viaje) (pineados []Viaje, esperando, huecos []string) {
	for _, ac := range acs {
		var head, browse *Viaje
		for i := range viajes {
			v := &viajes[i]
			if !cubre(v.AC, ac) {
				continue
			}
			if v.Modo == "browse" {
				if browse == nil {
					browse = v
				}
				continue
			}
			if v.Modo == "headless" && head == nil {
				head = v
			}
		}
		switch {
		case head != nil:
			if !yaEsta(pineados, head.ID) {
				pineados = append(pineados, *head)
			}
		case browse != nil:
			esperando = append(esperando, ac)
		default:
			huecos = append(huecos, ac)
		}
	}
	return pineados, esperando, huecos
}

func yaEsta(vs []Viaje, id string) bool {
	for _, v := range vs {
		if v.ID == id {
			return true
		}
	}
	return false
}

func cubre(acs []string, ac string) bool {
	for _, x := range acs {
		if x == ac {
			return true
		}
	}
	return false
}

func mismosAC(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for _, x := range a {
		if !cubre(b, x) {
			return false
		}
	}
	return true
}

var runnersPermitidos = []string{"npx playwright ", "npx jest ", "npm run e2e", "npm run test:e2e", "dotnet test"}

func CmdPermitido(cmd string) bool {
	c := strings.TrimSpace(cmd)
	if c == "" || strings.Contains(c, "exit 0") || strings.Contains(c, "touch ") {
		return false
	}
	for _, p := range runnersPermitidos {
		base := strings.TrimSpace(p)
		if c == base || strings.HasPrefix(c, base+" ") {
			return true
		}
	}
	return false
}

// PinCatalogo copia al estado los viajes headless que cubren los criterios.
// Lo que falta queda en Huecos. No crea el contenedor.
func PinCatalogo(st Store, id, catalogPath string, acs []string, ahora time.Time) error {
	w, err := st.Load(id)
	if err != nil {
		return err
	}
	cat, err := LeerCatalogo(catalogPath)
	if err != nil {
		return err
	}
	pineados, esperando, huecos := Matchear(acs, cat.Viajes)
	var gates []Gate
	for _, g := range w.Gates {
		if !strings.HasPrefix(g.Nombre, "viaje:") {
			gates = append(gates, g)
		}
	}
	w.Gates = gates
	w.Viajes = nil
	for _, v := range pineados {
		if !CmdPermitido(v.Cmd) {
			return &Error{Codigo: "viaje_cmd", Mensaje: v.ID}
		}
		w.Viajes = append(w.Viajes, ViajePin{ID: v.ID, AC: v.AC, Cmd: v.Cmd, Artefacto: v.Artefacto})
		w.Gates = append(w.Gates, Gate{Nombre: "viaje:" + v.ID, Cmd: CmdConEvidencia(v.Cmd, v.Artefacto), Hard: true})
	}
	w.Huecos = huecos
	w.Esperando = esperando
	w.Actualizado = ahora.Format(time.RFC3339)
	return st.Save(w)
}

func leerPropuestas(worktree, id string) ([]Viaje, error) {
	p := filepath.Join(worktree, "e2e", "propuestas", id+".json")
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var uno Viaje
	if err := json.Unmarshal(b, &uno); err != nil {
		return nil, &Error{Codigo: "propuesta_no_json", Mensaje: err.Error()}
	}
	if uno.ID != "" {
		return []Viaje{uno}, nil
	}
	var lista Catalogo
	if err := json.Unmarshal(b, &lista); err != nil {
		return nil, &Error{Codigo: "propuesta_no_json", Mensaje: err.Error()}
	}
	return lista.Viajes, nil
}

// journeyPasoOk acepta dos formatos de evidencia:
//   - el journey propio: ok en true y todos los pasos en ok.
//   - el reporte JSON de Playwright (--reporter=json): al menos un test pasó y
//     ninguno falló, quedó inestable ni fue salteado. Un test salteado no es
//     verde: es la forma más fácil de esconder una regresión.
func journeyPasoOk(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var j struct {
		OK    bool `json:"ok"`
		Pasos []struct {
			OK bool `json:"ok"`
		} `json:"pasos"`
		Stats *struct {
			Expected   int `json:"expected"`
			Skipped    int `json:"skipped"`
			Unexpected int `json:"unexpected"`
			Flaky      int `json:"flaky"`
		} `json:"stats"`
		// Reporte de jest (--json --outputFile).
		JestTotal    *int `json:"numTotalTests"`
		JestPasaron  int  `json:"numPassedTests"`
		JestFallaron int  `json:"numFailedTests"`
		JestSalteado int  `json:"numPendingTests"`
		JestTodo     int  `json:"numTodoTests"`
		JestSuitesKO int  `json:"numFailedTestSuites"`
	}
	if json.Unmarshal(b, &j) != nil {
		return false
	}
	if j.JestTotal != nil {
		return j.JestPasaron > 0 && j.JestFallaron == 0 && j.JestSalteado == 0 && j.JestTodo == 0 && j.JestSuitesKO == 0
	}
	if j.Stats != nil {
		return j.Stats.Expected > 0 && j.Stats.Skipped == 0 && j.Stats.Unexpected == 0 && j.Stats.Flaky == 0
	}
	if !j.OK || len(j.Pasos) == 0 {
		return false
	}
	for _, p := range j.Pasos {
		if !p.OK {
			return false
		}
	}
	return true
}

// esReporteConocido dice si el archivo es un reporte de Playwright, de jest o
// un journey. Un artefacto de otro formato solo se chequea por fecha.
func esReporteConocido(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(b, &m) != nil {
		return false
	}
	for _, k := range []string{"stats", "numTotalTests", "pasos"} {
		if _, ok := m[k]; ok {
			return true
		}
	}
	return false
}

// CmdConEvidencia hace que un viaje de Playwright deje su reporte JSON en el
// artefacto, sin tocar la config del repo: el spec es Playwright común y la
// evidencia la pide el reloj. Otros runners quedan como están.
func CmdConEvidencia(cmd, artefacto string) string {
	c := strings.TrimSpace(cmd)
	if artefacto == "" {
		return cmd
	}
	seguro := "'" + strings.ReplaceAll(artefacto, "'", `'\''`) + "'"
	if strings.HasPrefix(c, "npx jest ") {
		return "mkdir -p \"$(dirname " + seguro + ")\" && " + c + " --json --outputFile=" + seguro
	}
	if !strings.HasPrefix(c, "npx playwright ") {
		return cmd
	}
	return "mkdir -p \"$(dirname " + seguro + ")\" && PLAYWRIGHT_JSON_OUTPUT_FILE=" + seguro + " " + c + " --reporter=json"
}

func catalogoTiene(cat Catalogo, v Viaje) bool {
	for _, x := range cat.Viajes {
		if x.ID == v.ID && x.Cmd == v.Cmd && mismosAC(x.AC, v.AC) {
			return true
		}
	}
	return false
}

// aplicarViajes corre las propuestas nuevas y perdona un viaje pineado solo
// si el reemplazo pasó y cubre los mismos criterios. No anota fallos.
func aplicarViajes(ctx context.Context, w *WI, inf *Informe, opt Opciones, inicio time.Time) (string, error) {
	if len(w.Viajes) == 0 && len(w.Huecos) == 0 && len(w.Esperando) == 0 {
		return "", nil
	}
	props, err := leerPropuestas(w.Worktree, w.ID)
	if err != nil {
		return "", err
	}
	type pasada struct {
		v  Viaje
		ok bool
	}
	var hechas []pasada
	for _, p := range props {
		if !CmdPermitido(p.Cmd) {
			return "viaje_cmd", nil
		}
		exit, cola, e := opt.Runner.Run(ctx, w.Worktree, "propuesta:"+p.ID, CmdConEvidencia(p.Cmd, p.Artefacto))
		if e != nil {
			exit = -1
		}
		full, ferr := rutaDentro(w.Worktree, p.Artefacto)
		ok := exit == 0 && ferr == nil && artefactoFresco(w.Worktree, p.Artefacto, inicio) && journeyPasoOk(full)
		inf.Gates = append(inf.Gates, GateInforme{Nombre: "propuesta:" + p.ID, Exit: exit, Cola: cola})
		hechas = append(hechas, pasada{v: p, ok: ok})
	}
	marcarViajeSinEvidencia(w, inf, inicio)
	for i := range inf.Gates {
		g := &inf.Gates[i]
		if g.Exit == 0 || !strings.HasPrefix(g.Nombre, "viaje:") {
			continue
		}
		id := strings.TrimPrefix(g.Nombre, "viaje:")
		pin := buscarPin(w.Viajes, id)
		if pin == nil {
			continue
		}
		for _, h := range hechas {
			if h.ok && h.v.Reemplaza == id && mismosAC(pin.AC, h.v.AC) {
				g.Reemplazado = true
			}
		}
	}
	for _, h := range hechas {
		if !h.ok {
			return "propuesta_roja", nil
		}
		cat, err := LeerCatalogo(filepath.Join(w.Worktree, "e2e", "catalogo.json"))
		if err != nil {
			return "catalogo_sin_propuesta", nil
		}
		if !catalogoTiene(cat, h.v) {
			return "catalogo_sin_propuesta", nil
		}
	}
	cubiertos := map[string]bool{}
	for _, h := range hechas {
		if h.ok {
			for _, ac := range h.v.AC {
				cubiertos[ac] = true
			}
		}
	}
	for _, ac := range w.Huecos {
		if !cubiertos[ac] {
			return "ac_sin_viaje", nil
		}
	}
	if len(w.Esperando) > 0 {
		return "esperando-persona", nil
	}
	return "", nil
}

// marcarViajeSinEvidencia: exit 0 no alcanza. El journey tiene que haberse
// reescrito en esta corrida, con todos los pasos en ok.
func marcarViajeSinEvidencia(w *WI, inf *Informe, inicio time.Time) {
	for i := range inf.Gates {
		g := &inf.Gates[i]
		if g.Exit != 0 || !strings.HasPrefix(g.Nombre, "viaje:") {
			continue
		}
		pin := buscarPin(w.Viajes, strings.TrimPrefix(g.Nombre, "viaje:"))
		if pin == nil {
			continue
		}
		full, err := rutaDentro(w.Worktree, pin.Artefacto)
		if err != nil || !artefactoFresco(w.Worktree, pin.Artefacto, inicio) || !journeyPasoOk(full) {
			g.Exit = -1
			g.SinEvidencia = true
		}
	}
}

func buscarPin(vs []ViajePin, id string) *ViajePin {
	for i := range vs {
		if vs[i].ID == id {
			return &vs[i]
		}
	}
	return nil
}

func artefactoFresco(worktree, rel string, inicio time.Time) bool {
	full, err := rutaDentro(worktree, rel)
	if err != nil {
		return false
	}
	st, err := os.Stat(full)
	if err != nil {
		return false
	}
	return !st.ModTime().Before(inicio.Add(-time.Second))
}

func primerFallo(gates []GateInforme) string {
	for _, g := range gates {
		if g.Exit != 0 && !g.Preexisting && !g.Reemplazado {
			return g.Nombre
		}
	}
	return ""
}
