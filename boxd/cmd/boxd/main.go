// Command boxd es el reloj del host. Sin --aplicar no corre gates ni arma el PR.
// Este corte no ejecuta az: --salida registro anota los argv en el estado.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tomas1402/agent-box/boxd/internal/reloj"
)

// version se pisa al compilar: go build -ldflags "-X main.version=v0.1.0".
var version = "dev"

const ayuda = `boxd: el reloj de agent-box. Una caja por work item; el PR sale solo si los
gates dan cero cuando los corre boxd, no el agente.

  boxd [opciones] <verbo> [opciones del verbo]

Verbos
  caja alta --wi ID --worktree DIR --config harness.json [--titulo T]
                    admite el work item y, con --docker --aplicar, crea su caja
  caja baja --wi ID borra la caja (el worktree queda)
  preparar --wi ID  corre el setup del harness adentro de la caja (npm ci, etc.)
  paso --wi ID --gate NOMBRE
                    corre un gate para ver el avance; no publica
  publicar --wi ID [--descripcion ARCHIVO]
                    corre todos los gates duros y, si dan cero, comenta el work
                    item y abre el PR. Sin --salida ejecutar es un ensayo: no escribe nada
  estado --wi ID    el estado guardado de un work item
  lista [--observar] [--metricas]
                    todas las cajas: fase, etapa, RAM y CPU. Es lo que lee el panel
  latido [--wi ID]  recalcula la etapa mirando los archivos de la rama
  veredicto --wi ID --aprueba|--rechaza --quien NOMBRE [--nota TEXTO]
                    anota la revisión del commit actual, hecha por alguien que no es el autor
  destrabar --wi ID vuelve a habilitar publicar tras 3 fallos seguidos
  reabrir --wi ID   saca un work item de listo
  comentar --wi ID --informe JSON
                    vuelve a dejar el comentario de un publicar
  version

Los verbos que crean, borran o publican (caja alta, caja baja, preparar, publicar) no
hacen nada sin --aplicar: muestran lo que harían. paso y los de lectura corren siempre.
Las opciones generales pueden ir antes o después del verbo. Con --json sale el objeto completo.
El estado vive en --estado-dir (por defecto $AGENT_BOX_ESTADO o ~/.local/share/agent-box/wi).
Las opciones se pueden fijar en un archivo, una por línea (imagen=node:22, runtime=runc, docker=true):
$AGENT_BOX_CONF, ~/.config/agent-box/boxd.conf o /etc/agent-box/boxd.conf, el primero que exista.

Opciones
`

// cargarConf lee el archivo de opciones, si hay. Lo que se pase por línea de comandos gana.
func cargarConf(fs *flag.FlagSet) error {
	home, _ := os.UserHomeDir()
	for _, p := range []string{os.Getenv("AGENT_BOX_CONF"), home + "/.config/agent-box/boxd.conf", "/etc/agent-box/boxd.conf"} {
		if p == "" {
			continue
		}
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		for n, l := range strings.Split(string(b), "\n") {
			l = strings.TrimSpace(l)
			if l == "" || strings.HasPrefix(l, "#") {
				continue
			}
			k, v, ok := strings.Cut(l, "=")
			if !ok || fs.Lookup(strings.TrimSpace(k)) == nil {
				return fmt.Errorf("%s línea %d: opción desconocida %q", p, n+1, l)
			}
			if err := fs.Set(strings.TrimSpace(k), strings.Trim(strings.TrimSpace(v), `"'`)); err != nil {
				return fmt.Errorf("%s línea %d: %v", p, n+1, err)
			}
		}
		return nil
	}
	return nil
}

// globalesAdelante deja que una opción general vaya antes o después del verbo:
// `boxd lista --json` y `boxd --json lista` son lo mismo.
func globalesAdelante(fs *flag.FlagSet, args []string) []string {
	var adelante, resto []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		nombre := strings.TrimLeft(a, "-")
		valor := false
		if j := strings.IndexByte(nombre, '='); j >= 0 {
			nombre, valor = nombre[:j], true
		}
		f := fs.Lookup(nombre)
		if !strings.HasPrefix(a, "-") || a == "--" || f == nil {
			resto = append(resto, a)
			continue
		}
		adelante = append(adelante, a)
		if esBool, ok := f.Value.(interface{ IsBoolFlag() bool }); !valor && !(ok && esBool.IsBoolFlag()) && i+1 < len(args) {
			i++
			adelante = append(adelante, args[i])
		}
	}
	return append(adelante, resto...)
}

// dirEstadoPorDefecto es el mismo lugar donde lo busca el panel.
func dirEstadoPorDefecto() string {
	if d := os.Getenv("AGENT_BOX_ESTADO"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "agent-box-wi"
	}
	return home + "/.local/share/agent-box/wi"
}

func main() {
	os.Exit(Ejecutar(os.Args[1:], os.Stdout, os.Stderr))
}

func Ejecutar(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("boxd", flag.ContinueOnError)
	fs.SetOutput(stderr)
	jsonOut := fs.Bool("json", false, "un objeto JSON en stdout")
	aplicar := fs.Bool("aplicar", false, "correr gates y registrar la salida")
	dir := fs.String("estado-dir", "", "directorio del estado de los work items")
	nodo := fs.String("nodo", "", "nombre de esta máquina, solo para mostrar")
	presupuestoMB := fs.Int("presupuesto-mb", 12288, "RAM total que pueden tomar las cajas de esta máquina")
	burstMB := fs.Int("burst-mb", 5120, "techo de RAM de una caja mientras corre gates")
	edicionMB := fs.Int("edicion-mb", 1200, "techo de RAM de una caja mientras se edita")
	vivas := fs.String("vivas", "", "cajas vivas id:mb,id:mb para el presupuesto (con --docker se leen solas)")
	conDocker := fs.Bool("docker", false, "crear la caja y correr los gates adentro, con docker")
	imagen := fs.String("imagen", "", "imagen de la caja")
	runtime := fs.String("runtime", "", "runtime de docker: runsc para gVisor, runc para el común. Vacío usa el que tenga docker por defecto")
	red := fs.String("red", "", "red de docker de la caja")
	usuario := fs.String("usuario", "", "usuario que corre los gates adentro de la caja")
	salida := fs.String("salida", "registro", "registro: ensayo, solo anota los comandos. ejecutar: los corre con la CLI del proveedor (az o gh)")
	herramientas := fs.String("herramientas", "", "directorio del host que la caja ve en /opt/caja, solo lectura")
	supEnv := fs.String("supervisor-env", "/etc/agent-box/supervisor.env", "credenciales de escritura, fuera de la caja")
	fs.Bool("version", false, "igual que el verbo version")
	if err := cargarConf(fs); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if err := fs.Parse(globalesAdelante(fs, args)); err != nil {
		return 2
	}
	fs.Usage = func() { fmt.Fprint(stderr, ayuda); fs.PrintDefaults() }
	rest := fs.Args()
	if v := fs.Lookup("version"); v != nil && v.Value.String() == "true" {
		rest = []string{"version"}
	}
	if len(rest) > 0 && (rest[0] == "ayuda" || rest[0] == "help") {
		fs.SetOutput(stdout)
		fs.Usage()
		return 0
	}
	if len(rest) == 0 {
		fs.Usage()
		return 2
	}
	if rest[0] == "version" {
		return escribir(stdout, *jsonOut, "version", true, map[string]any{"version": version, "contrato": 1}, nil)
	}
	if *dir == "" {
		*dir = dirEstadoPorDefecto()
	}
	perfil := reloj.PerfilDe(*edicionMB, *burstMB, *presupuestoMB)
	lista, err := parseVivas(*vivas)
	if err != nil {
		return escribir(stdout, *jsonOut, rest[0], false, nil, err)
	}
	st := reloj.Store{Dir: *dir}
	ctx := context.Background()
	ahora := time.Now()
	dk := reloj.Docker{}
	if *salida == "az" || *salida == "gh" { // nombres viejos
		*salida = "ejecutar"
	}
	if *salida != "registro" && *salida != "ejecutar" {
		return escribir(stdout, *jsonOut, rest[0], false, nil, &reloj.Error{Codigo: "uso", Mensaje: "--salida registro|ejecutar"})
	}

	switch rest[0] {
	case "caja":
		if len(rest) >= 2 && rest[1] == "baja" {
			sub := flag.NewFlagSet("baja", flag.ContinueOnError)
			sub.SetOutput(stderr)
			wi := sub.String("wi", "", "")
			if err := sub.Parse(rest[2:]); err != nil {
				return 2
			}
			if !*aplicar || !*conDocker {
				return escribir(stdout, *jsonOut, "caja-baja", true, map[string]any{"codigo": "seco", "caja": "box-" + *wi}, nil)
			}
			if err := dk.Baja(ctx, *wi); err != nil {
				return escribir(stdout, *jsonOut, "caja-baja", false, nil, err)
			}
			return escribir(stdout, *jsonOut, "caja-baja", true, map[string]any{"caja": "box-" + *wi, "borrada": true}, nil)
		}
		if len(rest) < 2 || rest[1] != "alta" {
			return escribir(stdout, *jsonOut, "caja", false, nil, &reloj.Error{Codigo: "uso", Mensaje: "caja alta | caja baja"})
		}
		sub := flag.NewFlagSet("alta", flag.ContinueOnError)
		sub.SetOutput(stderr)
		wi := sub.String("wi", "", "")
		wt := sub.String("worktree", "", "")
		cfg := sub.String("config", "", "")
		base := sub.String("baseline", "", "")
		titulo := sub.String("titulo", "", "título del work item, para el PR")
		if err := sub.Parse(rest[2:]); err != nil {
			return 2
		}
		if !*aplicar {
			spec, err := reloj.PlanCaja(*wi, *wt, perfil)
			if err != nil {
				return escribir(stdout, *jsonOut, "caja-alta", false, nil, err)
			}
			return escribir(stdout, *jsonOut, "caja-alta", true, map[string]any{"codigo": "seco", "spec": spec}, nil)
		}
		spec, err := reloj.Alta(st, *wi, *wt, *cfg, *base, perfil, ahora, *titulo)
		if err != nil {
			return escribir(stdout, *jsonOut, "caja-alta", false, nil, err)
		}
		spec.Imagen, spec.Runtime, spec.Red = *imagen, *runtime, *red
		// Un `git worktree` tiene su .git en el clon: sin ese montaje git no anda adentro.
		if *herramientas != "" {
			spec.Montajes = append(spec.Montajes, *herramientas+":/opt/caja:ro")
		}
		if comun := (reloj.GitCLI{}).DirComun(ctx, *wt); comun != "" {
			spec.Montajes = append(spec.Montajes, comun+":"+comun)
		}
		creada := false
		if *conDocker {
			if err := dk.Crear(ctx, spec); err != nil {
				return escribir(stdout, *jsonOut, "caja-alta", false, map[string]any{"wi": *wi, "spec": spec}, err)
			}
			creada = true
		}
		return escribir(stdout, *jsonOut, "caja-alta", true, map[string]any{"wi": *wi, "fase": reloj.Admitido, "spec": spec, "creada": creada}, nil)
	case "publicar":
		sub := flag.NewFlagSet("publicar", flag.ContinueOnError)
		sub.SetOutput(stderr)
		wi := sub.String("wi", "", "")
		descArchivo := sub.String("descripcion", "", "archivo con la descripción del PR")
		if err := sub.Parse(rest[1:]); err != nil {
			return 2
		}
		desc := ""
		if *descArchivo != "" {
			b, err := os.ReadFile(*descArchivo)
			if err != nil {
				return escribir(stdout, *jsonOut, "publicar", false, nil, &reloj.Error{Codigo: "descripcion", Mensaje: "no se pudo leer el archivo"})
			}
			desc = string(b)
		}
		reg := &reloj.Registro{}
		// Sin --docker no hay caja: Mem queda nil para que el informe no diga
		// que la RAM subió, y los gates corren en el host.
		opt := reloj.Opciones{
			Aplicar: *aplicar,
			Perfil:  perfil,
			Vivas:   lista,
			Runner:  reloj.Bash{},
			Sonda:   reloj.SondaVacia{},
			Salida:  reg,
			Git:     reloj.GitCLI{},
			Ahora:   time.Now,

			Descripcion: desc,
			Ensayo:      *salida == "registro",
		}
		if *conDocker && *aplicar {
			if lista == nil {
				if opt.Vivas, err = dk.Vivas(ctx); err != nil {
					return escribir(stdout, *jsonOut, "publicar", false, nil, err)
				}
			}
			opt.Mem, opt.Sonda = dk, dk
			opt.Runner = reloj.EnCaja{Docker: dk, ID: *wi, Usuario: *usuario}
		}
		if *salida == "ejecutar" {
			opt.Salida = reloj.SalidaAz{EnvFile: *supEnv}
		}
		inf, err := reloj.Publicar(ctx, st, *wi, opt)
		if err != nil {
			return escribir(stdout, *jsonOut, "publicar", false, nil, err)
		}
		ok := inf.Publico || inf.Codigo == "seco" || inf.Codigo == "ensayo_verde"
		datos := map[string]any{"informe": inf, "argv": reg.Llamadas}
		if !ok && inf.Codigo != "" {
			return escribir(stdout, *jsonOut, "publicar", false, datos, &reloj.Error{Codigo: inf.Codigo})
		}
		return escribir(stdout, *jsonOut, "publicar", true, datos, nil)
	case "preparar":
		sub := flag.NewFlagSet("preparar", flag.ContinueOnError)
		sub.SetOutput(stderr)
		wi := sub.String("wi", "", "")
		if err := sub.Parse(rest[1:]); err != nil {
			return 2
		}
		if !*conDocker || !*aplicar {
			return escribir(stdout, *jsonOut, "preparar", false, nil, &reloj.Error{Codigo: "uso", Mensaje: "preparar exige --docker --aplicar"})
		}
		vv := lista
		if vv == nil {
			vv, _ = dk.Vivas(ctx)
		}
		inf, err := reloj.Preparar(ctx, st, *wi, reloj.Opciones{
			Perfil: perfil, Vivas: vv, Mem: dk, Git: reloj.GitCLI{}, Ahora: time.Now,
			Runner: reloj.EnCaja{Docker: dk, ID: *wi, Usuario: *usuario},
		})
		if err != nil {
			return escribir(stdout, *jsonOut, "preparar", false, nil, err)
		}
		if inf.Codigo != "" {
			return escribir(stdout, *jsonOut, "preparar", false, inf, &reloj.Error{Codigo: inf.Codigo})
		}
		return escribir(stdout, *jsonOut, "preparar", true, inf, nil)
	case "paso":
		sub := flag.NewFlagSet("paso", flag.ContinueOnError)
		sub.SetOutput(stderr)
		wi := sub.String("wi", "", "")
		gate := sub.String("gate", "", "")
		if err := sub.Parse(rest[1:]); err != nil {
			return 2
		}
		var corre reloj.Runner = reloj.Bash{}
		if *conDocker {
			corre = reloj.EnCaja{Docker: dk, ID: *wi, Usuario: *usuario}
			// Una suite no entra en el techo de edición: el paso también pide el burst.
			vv := lista
			if vv == nil {
				vv, _ = dk.Vivas(ctx)
			}
			if !reloj.CabeBurst(perfil, vv, *wi) {
				return escribir(stdout, *jsonOut, "paso", false, nil, &reloj.Error{Codigo: "presupuesto", Mensaje: "no hay RAM para el burst; esperar a que termine otra suite"})
			}
			if _, err := dk.Aplicar(*wi, perfil.BurstMB, 0); err != nil {
				return escribir(stdout, *jsonOut, "paso", false, nil, err)
			}
			defer func() { _, _ = dk.Aplicar(*wi, perfil.EdicionMaxMB, 0) }()
		}
		inf, err := reloj.Paso(ctx, st, *wi, *gate, corre, reloj.GitCLI{}, ahora)
		if err != nil {
			return escribir(stdout, *jsonOut, "paso", false, nil, err)
		}
		// Un gate rojo es un paso que no pasó: lo dice la salida y lo dice el código de retorno.
		if inf.Codigo == "" && len(inf.Gates) == 1 && inf.Gates[0].Exit != 0 && !inf.Gates[0].Preexisting {
			inf.Codigo = "gate_rojo"
		}
		if inf.Codigo != "" {
			return escribir(stdout, *jsonOut, "paso", false, inf, &reloj.Error{Codigo: inf.Codigo})
		}
		return escribir(stdout, *jsonOut, "paso", true, inf, nil)
	case "destrabar":
		sub := flag.NewFlagSet("destrabar", flag.ContinueOnError)
		sub.SetOutput(stderr)
		wi := sub.String("wi", "", "")
		if err := sub.Parse(rest[1:]); err != nil {
			return 2
		}
		w, err := reloj.Destrabar(st, *wi, ahora)
		if err != nil {
			return escribir(stdout, *jsonOut, "destrabar", false, nil, err)
		}
		return escribir(stdout, *jsonOut, "destrabar", true, w, nil)
	case "comentar":
		// Vuelve a dejar el comentario del reloj a partir del JSON de un publicar.
		sub := flag.NewFlagSet("comentar", flag.ContinueOnError)
		sub.SetOutput(stderr)
		wi := sub.String("wi", "", "")
		informe := sub.String("informe", "", "salida --json de un publicar")
		descArchivo := sub.String("descripcion", "", "")
		if err := sub.Parse(rest[1:]); err != nil {
			return 2
		}
		var previo struct {
			Datos struct {
				Informe reloj.Informe `json:"informe"`
			} `json:"datos"`
		}
		b, err := os.ReadFile(*informe)
		if err != nil || json.Unmarshal(b, &previo) != nil {
			return escribir(stdout, *jsonOut, "comentar", false, nil, &reloj.Error{Codigo: "informe", Mensaje: "no se pudo leer el informe"})
		}
		desc, _ := os.ReadFile(*descArchivo)
		var sal reloj.Salida = &reloj.Registro{}
		if *salida == "ejecutar" {
			sal = reloj.SalidaAz{EnvFile: *supEnv}
		}
		htmlTxt, err := reloj.Comentar(ctx, st, *wi, previo.Datos.Informe, reloj.Opciones{Git: reloj.GitCLI{}, Descripcion: string(desc), Salida: sal})
		if err != nil {
			return escribir(stdout, *jsonOut, "comentar", false, nil, err)
		}
		return escribir(stdout, *jsonOut, "comentar", true, map[string]any{"html": htmlTxt, "enviado": *salida == "ejecutar"}, nil)
	case "veredicto":
		// La revisión de alguien que no es el autor. Vale para el commit actual del worktree.
		sub := flag.NewFlagSet("veredicto", flag.ContinueOnError)
		sub.SetOutput(stderr)
		wi := sub.String("wi", "", "")
		aprueba := sub.Bool("aprueba", false, "")
		rechaza := sub.Bool("rechaza", false, "")
		quien := sub.String("quien", "", "quién revisó")
		nota := sub.String("nota", "", "")
		if err := sub.Parse(rest[1:]); err != nil {
			return 2
		}
		if *aprueba == *rechaza {
			return escribir(stdout, *jsonOut, "veredicto", false, nil, &reloj.Error{Codigo: "uso", Mensaje: "--aprueba o --rechaza"})
		}
		w, err := reloj.AnotarVeredicto(ctx, st, reloj.GitCLI{}, *wi, *aprueba, *quien, *nota, ahora)
		if err != nil {
			return escribir(stdout, *jsonOut, "veredicto", false, nil, err)
		}
		return escribir(stdout, *jsonOut, "veredicto", true, w, nil)
	case "reabrir":
		sub := flag.NewFlagSet("reabrir", flag.ContinueOnError)
		sub.SetOutput(stderr)
		wi := sub.String("wi", "", "")
		if err := sub.Parse(rest[1:]); err != nil {
			return 2
		}
		w, err := reloj.Reabrir(st, *wi, ahora)
		if err != nil {
			return escribir(stdout, *jsonOut, "reabrir", false, nil, err)
		}
		return escribir(stdout, *jsonOut, "reabrir", true, w, nil)
	case "estado":
		sub := flag.NewFlagSet("estado", flag.ContinueOnError)
		sub.SetOutput(stderr)
		wi := sub.String("wi", "", "")
		if err := sub.Parse(rest[1:]); err != nil {
			return 2
		}
		w, err := st.Load(*wi)
		if err != nil {
			return escribir(stdout, *jsonOut, "estado", false, nil, err)
		}
		return escribir(stdout, *jsonOut, "estado", true, w, nil)
	case "latido":
		// Sin --wi observa todos. Es lo que corre el timer del nodo.
		sub := flag.NewFlagSet("latido", flag.ContinueOnError)
		sub.SetOutput(stderr)
		wi := sub.String("wi", "", "")
		if err := sub.Parse(rest[1:]); err != nil {
			return 2
		}
		if *wi != "" {
			w, err := reloj.Latido(ctx, st, reloj.GitCLI{}, *wi, ahora)
			if err != nil {
				return escribir(stdout, *jsonOut, "latido", false, nil, err)
			}
			return escribir(stdout, *jsonOut, "latido", true, w, nil)
		}
		return escribir(stdout, *jsonOut, "latido", true, map[string]any{"cajas": listar(ctx, st, true, ahora)}, nil)
	case "lista":
		// Una sola llamada para el panel. Con --observar refresca la etapa antes.
		sub := flag.NewFlagSet("lista", flag.ContinueOnError)
		sub.SetOutput(stderr)
		observar := sub.Bool("observar", false, "recalcular la etapa de cada caja antes de listar")
		metricas := sub.Bool("metricas", false, "sumar RAM y CPU de cada caja viva y el presupuesto del nodo")
		if err := sub.Parse(rest[1:]); err != nil {
			return 2
		}
		cajas := listar(ctx, st, *observar, ahora)
		if !*metricas {
			return escribir(stdout, *jsonOut, "lista", true, map[string]any{"cajas": cajas}, nil)
		}
		// Un docker stats para todas: es lo caro (unos 2 s), por eso es opcional.
		m := dk.Metricas(ctx)
		type conMetrica struct {
			reloj.WI
			Metricas *reloj.Metrica `json:"metricas,omitempty"`
		}
		filas := make([]conMetrica, len(cajas))
		techos := 0
		for i, c := range cajas {
			filas[i].WI = c
			if x, ok := m[c.ID]; ok {
				x := x
				filas[i].Metricas = &x
				techos += x.TechoMB
			}
		}
		host, _ := os.Hostname()
		return escribir(stdout, *jsonOut, "lista", true, map[string]any{"cajas": filas, "nodo": map[string]any{
			"host": host, "perfil": *nodo, "presupuestoMb": perfil.PresupuestoMB, "burstMb": perfil.BurstMB,
			"vivas": len(m), "techosMb": techos,
		}}, nil)
	default:
		return escribir(stdout, *jsonOut, rest[0], false, nil, &reloj.Error{Codigo: "uso", Mensaje: rest[0]})
	}
}

// listar devuelve los WI. Con observar, corre el latido de todos en paralelo:
// un git status por worktree, sin esperar uno al otro.
func listar(ctx context.Context, st reloj.Store, observar bool, ahora time.Time) []reloj.WI {
	todos, _ := st.Lista()
	if !observar {
		if todos == nil {
			return []reloj.WI{}
		}
		return todos
	}
	out := make([]reloj.WI, len(todos))
	var wg sync.WaitGroup
	for i := range todos {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			w, err := reloj.Latido(ctx, st, reloj.GitCLI{}, todos[i].ID, ahora)
			if err != nil {
				w = todos[i]
			}
			out[i] = w
		}(i)
	}
	wg.Wait()
	return out
}

func parseVivas(s string) ([]reloj.CajaViva, error) {
	if s == "" {
		return nil, nil
	}
	var out []reloj.CajaViva
	for _, parte := range strings.Split(s, ",") {
		id, mb, ok := strings.Cut(parte, ":")
		if !ok {
			return nil, &reloj.Error{Codigo: "vivas", Mensaje: parte}
		}
		n, err := strconv.Atoi(mb)
		if err != nil {
			return nil, &reloj.Error{Codigo: "vivas", Mensaje: parte}
		}
		out = append(out, reloj.CajaViva{ID: id, MaxMB: n})
	}
	return out, nil
}

func escribir(stdout io.Writer, jsonOut bool, op string, ok bool, datos any, err error) int {
	codigo := 0
	if !ok {
		codigo = 3
	}
	if !jsonOut {
		cod, msj := "", ""
		if er, si := err.(*reloj.Error); si {
			cod, msj = er.Codigo, er.Mensaje
		} else if err != nil {
			cod, codigo = err.Error(), 1
		}
		humano(stdout, op, ok, datos, cod, msj)
		return codigo
	}
	resp := map[string]any{"contrato": 1, "ok": ok, "operacion": op, "datos": datos}
	if err != nil {
		if er, si := err.(*reloj.Error); si {
			resp["error"] = map[string]string{"codigo": er.Codigo, "mensaje": er.Mensaje}
		} else {
			resp["error"] = map[string]string{"codigo": "interno", "mensaje": err.Error()}
			codigo = 1
		}
	}
	enc := json.NewEncoder(stdout)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(resp)
	return codigo
}
