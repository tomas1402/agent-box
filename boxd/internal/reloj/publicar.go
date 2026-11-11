package reloj

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Runner interface {
	Run(ctx context.Context, dir, nombre, cmd string) (exit int, cola string, err error)
}

// Memoria aplica el techo. highAplicado es falso si el backend solo pudo
// escribir memory.max (docker update).
type Memoria interface {
	Aplicar(id string, maxMB, highMB int) (highAplicado bool, err error)
}

// Salida escribe el WI y abre el PR. Corte A no ejecuta az: el registro
// guarda los argv para revisarlos. El exec queda afuera hasta tener un WI de prueba.
type Salida interface {
	Comentar(ctx context.Context, argv []string) error
	AbrirPR(ctx context.Context, argv []string) (url string, err error)
}

type Opciones struct {
	Aplicar bool
	Perfil  Perfil
	Vivas   []CajaViva
	Runner  Runner
	Mem     Memoria
	Sonda   Sonda
	Salida  Salida
	// Git es opcional: con él, al terminar la corrida la etapa vuelve a salir
	// de los archivos del worktree.
	Git   Git
	Ahora func() time.Time
	// Ensayo: corre todo y arma los argv, pero no deja el WI listo. Un ensayo
	// verde no puede tapar la publicación de verdad.
	Ensayo bool
	// Descripcion es el texto del PR (qué cambió, cómo se valida). La escribe el
	// orquestador en el host; el reloj le agrega al final el resultado de los gates.
	Descripcion string
}

type GateInforme struct {
	Nombre       string `json:"nombre"`
	Exit         int    `json:"exit"`
	Preexisting  bool   `json:"preexisting"`
	Cola         string `json:"cola,omitempty"`
	MS           int64  `json:"ms"`
	Reemplazado  bool   `json:"reemplazado,omitempty"`
	SinEvidencia bool   `json:"sinEvidencia,omitempty"`
}

type Informe struct {
	WI        string        `json:"wi"`
	Fase      Fase          `json:"fase"`
	Codigo    string        `json:"codigo,omitempty"`
	Gates     []GateInforme `json:"gates"`
	E2ERuta   string        `json:"e2eRuta,omitempty"`
	E2EFresco bool          `json:"e2eFresco"`
	Detalle   string        `json:"detalle,omitempty"`
	Publico   bool          `json:"publico"`
	PR        string        `json:"pr,omitempty"`
	Memoria   MemInforme    `json:"memoria"`
}

type MemInforme struct {
	Subio        bool `json:"subio"`
	Bajo         bool `json:"bajo"`
	BurstMB      int  `json:"burstMB"`
	HighAplicado bool `json:"highAplicado"`
}

func (o Opciones) ahora() time.Time {
	if o.Ahora != nil {
		return o.Ahora()
	}
	return time.Now()
}

func Publicar(ctx context.Context, st Store, id string, opt Opciones) (inf Informe, err error) {
	w, err := st.Load(id)
	if err != nil {
		return Informe{}, err
	}
	inf.WI = id
	inf.Fase = w.Fase
	if w.Fase == Escalado {
		inf.Codigo = "escalado"
		return inf, nil
	}
	if w.Fase == Listo {
		inf.Codigo = "ya_listo"
		return inf, nil
	}
	if !opt.Aplicar {
		inf.Codigo = "seco"
		for _, g := range gatesDuros(w.Gates) {
			inf.Gates = append(inf.Gates, GateInforme{Nombre: g.Nombre, Exit: -1})
		}
		return inf, nil
	}
	if err := sondaLimpia(opt.Sonda, id); err != nil {
		if er, ok := err.(*Error); ok {
			inf.Codigo = er.Codigo
			return inf, nil
		}
		return inf, err
	}
	if err := opt.Perfil.validar(); err != nil {
		inf.Codigo = "perfil"
		return inf, nil
	}
	if !CabeBurst(opt.Perfil, opt.Vivas, id) {
		inf.Codigo = "presupuesto"
		return inf, nil
	}
	if opt.Runner == nil {
		inf.Codigo = "runner_ausente"
		return inf, nil
	}
	soltar, ocupado, err := st.Trabar(id, false)
	if err != nil {
		return inf, err
	}
	if ocupado {
		inf.Codigo = "en_curso"
		return inf, nil
	}
	defer soltar()
	if w, err = st.Load(id); err != nil {
		return inf, err
	}
	// Las reglas de proceso van antes que los gates: no gastan RAM y no cuentan como fallo.
	if cod, det := chequearReglas(ctx, w, opt.Git, opt.ahora()); cod != "" {
		inf.Codigo, inf.Detalle = cod, det
		return inf, nil
	}
	// El proveedor se valida antes de correr: no tiene sentido una suite de cinco
	// minutos para descubrir al final que el harness no dice a dónde va el PR.
	prov, hayProv := ProveedorDe(w.Tracker)
	if !sinProveedor(w.Tracker) {
		if !hayProv {
			inf.Codigo, inf.Detalle = "tracker_desconocido", "tracker.provider tiene que ser azure, github o ninguno"
			return inf, nil
		}
		if falta := o(prov.Falta(w), map[bool]string{true: "prTarget"}[w.PRTarget == ""]); falta != "" {
			inf.Codigo, inf.Detalle = "tracker_incompleto", "al harness le falta "+falta
			return inf, nil
		}
	}

	if opt.Mem != nil {
		highBurst := opt.Perfil.BurstMB * 8 / 10
		highOK, e := opt.Mem.Aplicar(id, opt.Perfil.BurstMB, highBurst)
		if e != nil {
			inf.Codigo = "memoria_subir"
			return inf, nil
		}
		inf.Memoria.Subio = true
		inf.Memoria.BurstMB = opt.Perfil.BurstMB
		inf.Memoria.HighAplicado = highOK
		defer func() {
			if _, e := opt.Mem.Aplicar(id, opt.Perfil.EdicionMaxMB, opt.Perfil.EdicionHighMB); e == nil {
				inf.Memoria.Bajo = true
			}
		}()
	}

	inicio := opt.ahora()
	t0 := time.Now()
	tiempos := map[string]int64{}
	opt.Runner = &vivo{st: st, w: &w, dentro: opt.Runner, ahora: opt.ahora, tiempos: tiempos}
	defer func() {
		cerrarCorrida(ctx, &w, &inf, "publicar", inicio, t0, tiempos, opt.Git, opt.ahora())
		if e := st.Save(w); e != nil && err == nil {
			err = e
		}
	}()
	for _, g := range gatesDuros(w.Gates) {
		exit, cola, e := opt.Runner.Run(ctx, w.Worktree, g.Nombre, g.Cmd)
		if e != nil {
			exit = -1
		}
		res := GateInforme{Nombre: g.Nombre, Exit: exit, Cola: cola}
		if preexisting(w.Baseline, g.Nombre, exit) {
			res.Preexisting = true
		}
		inf.Gates = append(inf.Gates, res)
	}
	codigoViaje, err := aplicarViajes(ctx, &w, &inf, opt, inicio)
	if err != nil {
		return inf, err
	}
	primero := primerFallo(inf.Gates)
	if primero != "" && inf.Codigo == "" && sinEvidencia(inf.Gates, primero) {
		inf.Codigo = "viaje_sin_evidencia"
	}
	if primero == "" && codigoViaje != "" && codigoViaje != "esperando-persona" {
		primero = codigoViaje
		inf.Codigo = codigoViaje
	}

	ruta, e2eCodigo := chequearE2E(w, inicio)
	inf.E2ERuta = ruta
	inf.E2EFresco = e2eCodigo == ""
	if primero == "" && e2eCodigo != "" {
		primero = "e2e"
		inf.Codigo = e2eCodigo
	}
	// Browse no tapa un gate rojo ni un e2e viejo, y no suma fallos.
	if codigoViaje == "esperando-persona" && primero == "" {
		inf.Codigo = codigoViaje
		inf.Fase = w.Fase
		return inf, nil
	}

	ahora := opt.ahora()
	if primero != "" {
		if inf.Codigo == "" {
			inf.Codigo = "gates_rojos"
		}
		anotarFallo(&w, primero, ahora)
		inf.Fase = w.Fase
		if err := st.Save(w); err != nil {
			return inf, err
		}
		return inf, nil
	}

	if sinProveedor(w.Tracker) {
		// Sin tracker: los gates deciden y no se escribe afuera.
		if opt.Ensayo {
			w.evento(ahora, "ensayo", "verde", "")
			inf.Codigo, inf.Fase = "ensayo_verde", w.Fase
			return inf, nil
		}
		marcarListo(&w, ahora)
		inf.Publico, inf.Fase = true, Listo
		return inf, st.Save(w)
	}
	if opt.Salida == nil {
		anotarFallo(&w, "credencial", ahora)
		inf.Codigo = "credencial_ausente"
		inf.Fase = w.Fase
		if err := st.Save(w); err != nil {
			return inf, err
		}
		return inf, nil
	}

	if r, ok := opt.Git.(interface {
		Rama(context.Context, string) string
	}); ok {
		w.Rama = r.Rama(ctx, w.Worktree)
	}
	w.ponerEtapa(EtapaPublicando, "comentario en el WI y PR", ahora)
	_ = st.Save(w)
	rama := o(w.Rama, w.BranchPrefix+w.ID)
	descripcion := "Reloj: gates duros en cero. Nada fue mergeado."
	if opt.Descripcion != "" {
		descripcion = strings.TrimSpace(opt.Descripcion) + "\n\n" + resumenGates(inf.Gates)
	}
	if err := opt.Salida.Comentar(ctx, prov.Comentario(w, armarDiscusion(ctx, w, inf, opt))); err != nil {
		anotarFallo(&w, "wi", ahora)
		inf.Codigo = "wi"
		inf.Fase = w.Fase
		if e := st.Save(w); e != nil {
			return inf, e
		}
		return inf, nil
	}
	url, err := opt.Salida.AbrirPR(ctx, prov.PR(w, rama, tituloPR(w), descripcion))
	if u := prov.URL(url); u != "" {
		url = u
	}
	if err != nil {
		anotarFallo(&w, "pr", ahora)
		inf.Codigo = "pr"
		inf.Fase = w.Fase
		if e := st.Save(w); e != nil {
			return inf, e
		}
		return inf, nil
	}
	if opt.Ensayo {
		w.evento(ahora, "ensayo", "verde", "")
		inf.Codigo = "ensayo_verde"
		inf.Fase = w.Fase
		return inf, nil
	}
	marcarListo(&w, ahora)
	if err := st.Save(w); err != nil {
		return inf, err
	}
	inf.Publico = true
	inf.PR = url
	inf.Fase = Listo
	inf.Codigo = ""
	return inf, nil
}

func sinEvidencia(gates []GateInforme, nombre string) bool {
	for _, g := range gates {
		if g.Nombre == nombre && g.SinEvidencia {
			return true
		}
	}
	return false
}

func chequearE2E(w WI, inicio time.Time) (string, string) {
	if w.E2E == "" {
		return "", "e2e_sin_declarar"
	}
	full, err := rutaDentro(w.Worktree, w.E2E)
	if err != nil {
		return "", "e2e_fuera"
	}
	st, err := os.Stat(full)
	if err != nil {
		return full, "e2e_ausente"
	}
	limite := inicio.Add(-time.Second)
	if st.ModTime().Before(limite) {
		return full, "e2e_viejo"
	}
	if esReporteConocido(full) && !journeyPasoOk(full) {
		return full, "e2e_rojo"
	}
	return full, ""
}

func rutaDentro(raiz, rel string) (string, error) {
	if rel == "" || filepath.IsAbs(rel) {
		return "", &Error{Codigo: "e2e_fuera"}
	}
	clean := filepath.Clean(rel)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return "", &Error{Codigo: "e2e_fuera"}
	}
	full := filepath.Join(raiz, clean)
	back, err := filepath.Rel(raiz, full)
	if err != nil || back == ".." || strings.HasPrefix(back, ".."+string(os.PathSeparator)) {
		return "", &Error{Codigo: "e2e_fuera"}
	}
	return full, nil
}

// resumenGates es la línea que el reloj agrega al PR: lo que corrió él, no lo que dice el agente.
func resumenGates(gates []GateInforme) string {
	var b strings.Builder
	b.WriteString("Gates corridos por el reloj, fuera de la sesión del agente: ")
	for i, g := range gates {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%s %d (%d s)", g.Nombre, g.Exit, g.MS/1000)
	}
	b.WriteString(". Nada fue mergeado.")
	return b.String()
}

func o(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// argsDestino agrega la organización y el proyecto si el harness los trae.
// Sin ellos az usa los defaults de quien lo corre, que en el host pueden no existir.
func argsDestino(t Tracker, conProyecto bool) []string {
	var out []string
	if t.OrgURL != "" {
		out = append(out, "--org", t.OrgURL)
	}
	if conProyecto && t.Project != "" {
		out = append(out, "--project", t.Project)
	}
	return out
}

// tituloPR usa el título del work item, con el prefijo del harness una sola vez.
func tituloPR(w WI) string {
	if w.Titulo == "" {
		return w.PRTitlePrefix + "WI " + w.ID
	}
	if strings.HasPrefix(w.Titulo, strings.TrimSpace(w.PRTitlePrefix)) {
		return w.Titulo
	}
	return w.PRTitlePrefix + w.Titulo
}

// Alta copia el config al estado. No crea el contenedor.
func Alta(st Store, id, worktree, configPath, baselinePath string, p Perfil, ahora time.Time, titulo ...string) (Spec, error) {
	spec, err := PlanCaja(id, worktree, p)
	if err != nil {
		return Spec{}, err
	}
	if prev, err := st.Load(id); err == nil && prev.Fase == Listo {
		return Spec{}, &Error{Codigo: "ya_listo", Mensaje: "el WI ya publicó"}
	} else if err != nil {
		if er, ok := err.(*Error); !ok || er.Codigo != "wi_no_admitido" {
			return Spec{}, err
		}
	}
	pin, err := LeerConfig(configPath)
	if err != nil {
		return Spec{}, err
	}
	base, err := LeerBaseline(baselinePath)
	if err != nil {
		return Spec{}, err
	}
	w := pin
	w.ID = id
	if len(titulo) > 0 {
		w.Titulo = titulo[0]
	}
	w.Worktree = worktree
	w.Baseline = base
	w.ponerFase(Admitido, ahora, "")
	if err := st.Save(w); err != nil {
		return Spec{}, err
	}
	return spec, nil
}

// Paso corre un gate para ver el avance. No publica y no cuenta como salida.
func Paso(ctx context.Context, st Store, id, gate string, run Runner, g Git, ahora time.Time) (Informe, error) {
	w, err := st.Load(id)
	if err != nil {
		return Informe{}, err
	}
	if w.Fase == Escalado || w.Fase == Listo {
		return Informe{WI: id, Fase: w.Fase, Codigo: "paso_cerrado"}, nil
	}
	var elegido *Gate
	for i := range w.Gates {
		if w.Gates[i].Nombre == gate {
			elegido = &w.Gates[i]
			break
		}
	}
	if elegido == nil {
		return Informe{}, &Error{Codigo: "gate_desconocido", Mensaje: gate}
	}
	if run == nil {
		return Informe{}, &Error{Codigo: "runner_ausente"}
	}
	soltar, ocupado, err := st.Trabar(id, false)
	if err != nil {
		return Informe{}, err
	}
	if ocupado {
		return Informe{WI: id, Fase: w.Fase, Codigo: "en_curso"}, nil
	}
	defer soltar()
	nombre, cmd := elegido.Nombre, elegido.Cmd
	if w, err = st.Load(id); err != nil {
		return Informe{}, err
	}
	t0 := time.Now()
	tiempos := map[string]int64{}
	v := &vivo{st: st, w: &w, dentro: run, ahora: func() time.Time { return ahora }, tiempos: tiempos}
	exit, cola, e := v.Run(ctx, w.Worktree, nombre, cmd)
	if e != nil {
		exit = -1
	}
	if w.Fase == Admitido {
		w.ponerFase(Trabajando, ahora, "primer paso")
	}
	w.UltimoPaso = gate
	w.Actualizado = ahora.Format(time.RFC3339)
	inf := Informe{
		WI: id, Fase: w.Fase,
		Gates: []GateInforme{{Nombre: gate, Exit: exit, Cola: cola, Preexisting: preexisting(w.Baseline, gate, exit)}},
	}
	cerrarCorrida(ctx, &w, &inf, "paso", ahora, t0, tiempos, g, ahora)
	if err := st.Save(w); err != nil {
		return Informe{}, err
	}
	return inf, nil
}

// Preparar corre los comandos de setup del harness adentro de la caja, con el
// techo de RAM del burst: un npm ci no entra en el perfil de edición.
func Preparar(ctx context.Context, st Store, id string, opt Opciones) (inf Informe, err error) {
	soltar, ocupado, err := st.Trabar(id, false)
	if err != nil {
		return Informe{}, err
	}
	w, err := st.Load(id)
	if err != nil {
		if soltar != nil {
			soltar()
		}
		return Informe{}, err
	}
	inf = Informe{WI: id, Fase: w.Fase}
	if ocupado {
		inf.Codigo = "en_curso"
		return inf, nil
	}
	defer soltar()
	if opt.Runner == nil {
		inf.Codigo = "runner_ausente"
		return inf, nil
	}
	if !CabeBurst(opt.Perfil, opt.Vivas, id) {
		inf.Codigo = "presupuesto"
		return inf, nil
	}
	if opt.Mem != nil {
		if _, e := opt.Mem.Aplicar(id, opt.Perfil.BurstMB, opt.Perfil.BurstMB*8/10); e != nil {
			inf.Codigo = "memoria_subir"
			return inf, nil
		}
		inf.Memoria.Subio, inf.Memoria.BurstMB = true, opt.Perfil.BurstMB
		defer func() {
			if _, e := opt.Mem.Aplicar(id, opt.Perfil.EdicionMaxMB, opt.Perfil.EdicionHighMB); e == nil {
				inf.Memoria.Bajo = true
			}
		}()
	}
	t0 := time.Now()
	tiempos := map[string]int64{}
	v := &vivo{st: st, w: &w, dentro: opt.Runner, ahora: opt.ahora, tiempos: tiempos}
	for i, cmd := range w.Setup {
		nombre := fmt.Sprintf("setup:%d", i+1)
		exit, cola, e := v.Run(ctx, w.Worktree, nombre, cmd)
		if e != nil {
			exit = -1
		}
		inf.Gates = append(inf.Gates, GateInforme{Nombre: nombre, Exit: exit, Cola: cola})
		if exit != 0 {
			inf.Codigo = "setup_rojo"
			break
		}
	}
	cerrarCorrida(ctx, &w, &inf, "preparar", opt.ahora(), t0, tiempos, opt.Git, opt.ahora())
	return inf, st.Save(w)
}
