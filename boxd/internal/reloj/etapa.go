package reloj

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Etapa es en qué está la caja ahora. No la declara el agente: sale de los
// archivos que tocó (latido) o de lo que boxd está corriendo (publicar, paso).
type Etapa string

const (
	EtapaSinCambios Etapa = "sin-cambios"
	EtapaCodigo     Etapa = "codigo"
	EtapaTests      Etapa = "tests"
	EtapaE2E        Etapa = "e2e"
	EtapaDocs       Etapa = "docs"
	EtapaGates      Etapa = "gates"
	EtapaPublicando Etapa = "publicando"
	EtapaInactiva   Etapa = "inactiva"
)

// Pasado este tiempo sin tocar un archivo, la caja figura inactiva.
const InactivaTras = 10 * time.Minute

const topeEventos = 50

type Actividad struct {
	Etapa   Etapa  `json:"etapa"`
	Detalle string `json:"detalle,omitempty"`
	Desde   string `json:"desde"`
	// UltimoCambio es el mtime del archivo más reciente de la rama.
	UltimoCambio string `json:"ultimoCambio,omitempty"`
	// Archivos cuenta lo que la rama tocó, por clase (codigo, tests, e2e, docs).
	Archivos map[Etapa]int `json:"archivos,omitempty"`
}

type Evento struct {
	TS      string `json:"ts"`
	Que     string `json:"que"` // fase, etapa o gate
	Valor   string `json:"valor"`
	Detalle string `json:"detalle,omitempty"`
}

// Corrida es el resumen de la última vez que boxd corrió los gates.
type Corrida struct {
	Tipo   string         `json:"tipo"` // publicar o paso
	Inicio string         `json:"inicio"`
	MS     int64          `json:"ms"`
	Codigo string         `json:"codigo,omitempty"`
	Gates  []GateResumido `json:"gates"`
}

type GateResumido struct {
	Nombre string `json:"nombre"`
	Exit   int    `json:"exit"`
	MS     int64  `json:"ms"`
	Verde  bool   `json:"verde"`
}

func (w *WI) evento(ahora time.Time, que, valor, detalle string) {
	w.Eventos = append(w.Eventos, Evento{TS: ahora.Format(time.RFC3339), Que: que, Valor: valor, Detalle: detalle})
	if n := len(w.Eventos); n > topeEventos {
		w.Eventos = append([]Evento(nil), w.Eventos[n-topeEventos:]...)
	}
}

// ponerFase cambia la fase y deja el evento solo si cambió.
func (w *WI) ponerFase(f Fase, ahora time.Time, detalle string) {
	if w.Fase != f {
		w.evento(ahora, "fase", string(f), detalle)
	}
	w.Fase = f
	w.Actualizado = ahora.Format(time.RFC3339)
}

// ponerEtapa cambia la etapa. Devuelve si cambió algo que valga un evento.
func (w *WI) ponerEtapa(e Etapa, detalle string, ahora time.Time) bool {
	if w.Actividad != nil && w.Actividad.Etapa == e && w.Actividad.Detalle == detalle {
		return false
	}
	previa := w.Actividad
	w.Actividad = &Actividad{Etapa: e, Detalle: detalle, Desde: ahora.Format(time.RFC3339)}
	if previa != nil {
		w.Actividad.Archivos = previa.Archivos
		w.Actividad.UltimoCambio = previa.UltimoCambio
	}
	if previa == nil || previa.Etapa != e {
		w.evento(ahora, "etapa", string(e), detalle)
	}
	return true
}

func etapaDeGate(nombre string) Etapa {
	bajo := strings.ToLower(nombre)
	if strings.HasPrefix(bajo, "viaje:") || strings.HasPrefix(bajo, "propuesta:") || strings.Contains(bajo, "e2e") {
		return EtapaE2E
	}
	return EtapaGates
}

// Clasificar dice a qué clase de trabajo pertenece un archivo del repo.
func Clasificar(rel string) Etapa {
	p := strings.ToLower(filepath.ToSlash(rel))
	base := p
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		base = p[i+1:]
	}
	segs := strings.Split(p, "/")
	tiene := func(s string) bool {
		for _, x := range segs[:len(segs)-1] {
			if x == s {
				return true
			}
		}
		return false
	}
	switch {
	case tiene("e2e") || tiene("playwright") || tiene("journeys") ||
		strings.Contains(base, ".e2e.") || strings.Contains(base, ".e2e-") || strings.HasPrefix(base, "playwright.config"):
		return EtapaE2E
	case tiene("test") || tiene("tests") || tiene("__tests__") || tiene("spec") || tiene("__mocks__") || tiene("fixtures") ||
		strings.Contains(base, ".test.") || strings.Contains(base, ".spec.") ||
		strings.HasSuffix(base, "_test.go") || strings.HasSuffix(base, "tests.cs") || strings.HasSuffix(base, "test.cs"):
		return EtapaTests
	case strings.HasSuffix(base, ".md") || tiene("docs"):
		return EtapaDocs
	}
	return EtapaCodigo
}

// Git lista los archivos que la rama tocó. Es una interfaz para testear sin repo.
type Git interface {
	Tocados(ctx context.Context, worktree, base string) ([]string, error)
}

// Rama devuelve la rama del worktree, o vacío si no se puede leer.
func (GitCLI) Rama(ctx context.Context, worktree string) string {
	out, err := exec.CommandContext(ctx, "git", "-C", worktree, "rev-parse", "--abbrev-ref", "HEAD").Output()
	r := strings.TrimSpace(string(out))
	if err != nil || r == "HEAD" {
		return ""
	}
	return r
}

// DirComun devuelve el .git del clon cuando el worktree es un `git worktree`.
// Ese directorio queda fuera del worktree: sin montarlo, git no anda en la caja.
func (GitCLI) DirComun(ctx context.Context, worktree string) string {
	out, err := exec.CommandContext(ctx, "git", "-C", worktree, "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
	d := strings.TrimSpace(string(out))
	if err != nil || d == "" || strings.HasPrefix(d, worktree+string(os.PathSeparator)) {
		return ""
	}
	return d
}

type GitCLI struct{}

// Tocados junta lo sin commitear (status) con lo commiteado desde la base.
// Si la base no existe en el clon, alcanza con el status.
func (GitCLI) Tocados(ctx context.Context, worktree, base string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	vistos := map[string]bool{}
	out, err := exec.CommandContext(ctx, "git", "-C", worktree, "status", "--porcelain", "-z", "--untracked-files=all").Output()
	if err != nil {
		return nil, &Error{Codigo: "git", Mensaje: "git status falló en " + worktree}
	}
	campos := bytes.Split(out, []byte{0})
	for i := 0; i < len(campos); i++ {
		c := campos[i]
		if len(c) < 4 {
			continue
		}
		xy, ruta := string(c[:2]), string(c[3:])
		vistos[ruta] = true
		// Un rename trae el nombre viejo en el campo siguiente.
		if xy[0] == 'R' || xy[0] == 'C' {
			i++
		}
	}
	// Sin remoto (un repo recién creado) la base es la rama local del mismo nombre.
	if base != "" && exec.CommandContext(ctx, "git", "-C", worktree, "rev-parse", "--verify", "-q", base).Run() != nil {
		base = strings.TrimPrefix(base, "origin/")
	}
	if base != "" {
		if out, err := exec.CommandContext(ctx, "git", "-C", worktree, "diff", "--name-only", "-z", base+"...HEAD").Output(); err == nil {
			for _, c := range bytes.Split(out, []byte{0}) {
				if len(c) > 0 {
					vistos[string(c)] = true
				}
			}
		}
	}
	lista := make([]string, 0, len(vistos))
	for r := range vistos {
		lista = append(lista, r)
	}
	sort.Strings(lista)
	return lista, nil
}

// Observar arma la actividad de un worktree mirando los archivos. No escribe.
func Observar(ctx context.Context, g Git, w WI, ahora time.Time) (Actividad, error) {
	base := ""
	if w.PRTarget != "" {
		base = "origin/" + w.PRTarget
	}
	tocados, err := g.Tocados(ctx, w.Worktree, base)
	if err != nil {
		return Actividad{}, err
	}
	a := Actividad{Etapa: EtapaSinCambios, Desde: ahora.Format(time.RFC3339)}
	if len(tocados) == 0 {
		return a, nil
	}
	a.Archivos = map[Etapa]int{}
	var ultimo time.Time
	var rutaUltimo string
	for _, rel := range tocados {
		a.Archivos[Clasificar(rel)]++
		st, err := os.Stat(filepath.Join(w.Worktree, rel))
		if err != nil {
			continue // borrado
		}
		if st.ModTime().After(ultimo) {
			ultimo, rutaUltimo = st.ModTime(), rel
		}
	}
	if rutaUltimo == "" {
		a.Etapa = EtapaCodigo
		return a, nil
	}
	a.UltimoCambio = ultimo.Format(time.RFC3339)
	a.Detalle = rutaUltimo
	a.Etapa = Clasificar(rutaUltimo)
	if ahora.Sub(ultimo) > InactivaTras {
		a.Etapa = EtapaInactiva
	}
	return a, nil
}

// Latido observa el worktree y guarda la actividad si la etapa cambió.
// Si boxd está corriendo gates sobre ese WI, no pisa nada y devuelve lo guardado.
func Latido(ctx context.Context, st Store, g Git, id string, ahora time.Time) (WI, error) {
	suelta, ocupado, err := st.Trabar(id, false)
	if err != nil {
		return WI{}, err
	}
	if ocupado {
		return st.Load(id)
	}
	defer suelta()
	w, err := st.Load(id)
	if err != nil {
		return WI{}, err
	}
	if w.Fase == Listo || w.Fase == Escalado {
		return w, nil
	}
	a, err := Observar(ctx, g, w, ahora)
	if err != nil {
		return w, err
	}
	cambio := w.ponerEtapa(a.Etapa, a.Detalle, ahora)
	w.Actividad.Archivos = a.Archivos
	w.Actividad.UltimoCambio = a.UltimoCambio
	if w.Fase == Admitido && a.Etapa != EtapaSinCambios {
		w.ponerFase(Trabajando, ahora, "primer archivo tocado")
		cambio = true
	}
	if !cambio {
		return w, nil
	}
	return w, st.Save(w)
}

// Lista devuelve todos los WI del directorio, ordenados por id.
func (s Store) Lista() ([]WI, error) {
	entradas, err := os.ReadDir(s.Dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []WI
	for _, e := range entradas {
		nombre := e.Name()
		if e.IsDir() || !strings.HasSuffix(nombre, ".json") {
			continue
		}
		w, err := s.Load(strings.TrimSuffix(nombre, ".json"))
		if err != nil {
			continue // un archivo roto no tapa al resto
		}
		out = append(out, w)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
