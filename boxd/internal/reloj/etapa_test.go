package reloj

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

type gitFalso struct{ archivos []string }

func (g gitFalso) Tocados(context.Context, string, string) ([]string, error) { return g.archivos, nil }

func TestClasificarSeparaCodigoTestsE2EYDocs(t *testing.T) {
	casos := map[string]Etapa{
		"src/modules/chat/chat.service.ts":            EtapaCodigo,
		"src/modules/chat/chat.service.spec.ts":       EtapaTests,
		"test/app.e2e-spec.ts":                        EtapaE2E,
		"e2e/admin-no-edita.spec.ts":                  EtapaE2E,
		"e2e/catalogo.json":                           EtapaE2E,
		"apps/web/tests/playwright/login.spec.ts":     EtapaE2E,
		"apps/web/playwright.config.ts":               EtapaE2E,
		"Api.Tests/Pedidos/PedidosControllerTests.cs": EtapaTests,
		"boxd/internal/reloj/etapa_test.go":           EtapaTests,
		"docs/plan.md":                                EtapaDocs,
		"README.md":                                   EtapaDocs,
		"package.json":                                EtapaCodigo,
	}
	for ruta, quiero := range casos {
		if got := Clasificar(ruta); got != quiero {
			t.Errorf("%s: %s, quería %s", ruta, got, quiero)
		}
	}
}

func tocar(t *testing.T, raiz, rel string, cuando time.Time) {
	t.Helper()
	p := filepath.Join(raiz, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, cuando, cuando); err != nil {
		t.Fatal(err)
	}
}

func TestLatidoSigueAlArchivoMasRecienteYDejaHistoria(t *testing.T) {
	st, wt := preparar(t, cfgBueno)
	ahora := ahoraFijo()
	ctx := context.Background()

	w, err := Latido(ctx, st, gitFalso{}, "123", ahora)
	if err != nil {
		t.Fatal(err)
	}
	if w.Fase != Admitido || w.Actividad.Etapa != EtapaSinCambios {
		t.Fatalf("sin archivos: fase %s etapa %s", w.Fase, w.Actividad.Etapa)
	}

	tocar(t, wt, "src/a.ts", ahora.Add(-2*time.Minute))
	w, _ = Latido(ctx, st, gitFalso{[]string{"src/a.ts"}}, "123", ahora)
	if w.Fase != Trabajando || w.Actividad.Etapa != EtapaCodigo {
		t.Fatalf("código: fase %s etapa %s", w.Fase, w.Actividad.Etapa)
	}

	tocar(t, wt, "e2e/viaje.spec.ts", ahora.Add(-time.Minute))
	w, _ = Latido(ctx, st, gitFalso{[]string{"src/a.ts", "e2e/viaje.spec.ts"}}, "123", ahora)
	if w.Actividad.Etapa != EtapaE2E || w.Actividad.Detalle != "e2e/viaje.spec.ts" {
		t.Fatalf("e2e: %+v", w.Actividad)
	}
	if w.Actividad.Archivos[EtapaCodigo] != 1 || w.Actividad.Archivos[EtapaE2E] != 1 {
		t.Fatalf("conteo: %v", w.Actividad.Archivos)
	}

	w, _ = Latido(ctx, st, gitFalso{[]string{"src/a.ts", "e2e/viaje.spec.ts"}}, "123", ahora.Add(time.Hour))
	if w.Actividad.Etapa != EtapaInactiva {
		t.Fatalf("una hora después: %s", w.Actividad.Etapa)
	}

	var etapas []string
	for _, e := range w.Eventos {
		if e.Que == "etapa" {
			etapas = append(etapas, e.Valor)
		}
	}
	quiero := []string{"sin-cambios", "codigo", "e2e", "inactiva"}
	if len(etapas) != len(quiero) {
		t.Fatalf("historia %v", etapas)
	}
	for i := range quiero {
		if etapas[i] != quiero[i] {
			t.Fatalf("historia %v", etapas)
		}
	}
	guardado, _ := st.Load("123")
	if guardado.Actividad == nil || guardado.Actividad.Etapa != EtapaInactiva {
		t.Fatal("el latido no quedó en el archivo")
	}
}

// espia mira el archivo de estado mientras el gate está corriendo.
type espia struct {
	st    Store
	visto []string
	exit  int
}

func (e *espia) Run(_ context.Context, dir, nombre, _ string) (int, string, error) {
	w, _ := e.st.Load("123")
	if w.Actividad != nil {
		e.visto = append(e.visto, string(w.Actividad.Etapa)+":"+w.Actividad.Detalle)
	}
	_ = os.MkdirAll(filepath.Join(dir, "e2e"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "e2e", "report.json"), []byte("{}"), 0o644)
	return e.exit, "", nil
}

func TestPublicarMuestraElGateMientrasCorreYDejaLaCorrida(t *testing.T) {
	st, _ := preparar(t, cfgBueno)
	e := &espia{st: st}
	inf, err := Publicar(context.Background(), st, "123", Opciones{
		Aplicar: true, Perfil: PerfilPorDefecto(), Runner: e, Sonda: SondaVacia{}, Salida: &Registro{}, Ahora: time.Now,
	})
	if err != nil || !inf.Publico {
		t.Fatalf("no publicó: %+v %v", inf, err)
	}
	if len(e.visto) != 1 || e.visto[0] != "gates:test" {
		t.Fatalf("durante el gate el estado decía %v", e.visto)
	}
	w, _ := st.Load("123")
	if w.Corrida == nil || w.Corrida.Tipo != "publicar" || len(w.Corrida.Gates) != 1 || !w.Corrida.Gates[0].Verde {
		t.Fatalf("corrida: %+v", w.Corrida)
	}
	if w.Actividad != nil {
		t.Fatalf("un WI listo no tiene actividad: %+v", w.Actividad)
	}
}

func TestPublicarRojoDejaCorridaYNoQuedaEnGates(t *testing.T) {
	st, _ := preparar(t, cfgBueno)
	inf, err := Publicar(context.Background(), st, "123", Opciones{
		Aplicar: true, Perfil: PerfilPorDefecto(), Runner: &espia{st: st, exit: 1}, Sonda: SondaVacia{}, Salida: &Registro{}, Ahora: time.Now,
	})
	if err != nil || inf.Publico {
		t.Fatalf("%+v %v", inf, err)
	}
	w, _ := st.Load("123")
	if w.Fase != Frenado || w.Corrida == nil || w.Corrida.Codigo != "gates_rojos" || w.Corrida.Gates[0].Verde {
		t.Fatalf("fase %s corrida %+v", w.Fase, w.Corrida)
	}
	if w.Actividad == nil || w.Actividad.Etapa == EtapaGates {
		t.Fatalf("la etapa quedó pegada en gates: %+v", w.Actividad)
	}
}

func TestDosPublicarALaVezElSegundoNoCorre(t *testing.T) {
	st, _ := preparar(t, cfgBueno)
	soltar, ocupado, err := st.Trabar("123", false)
	if err != nil || ocupado {
		t.Fatal(err, ocupado)
	}
	defer soltar()
	c := &cuenta{}
	inf, err := Publicar(context.Background(), st, "123", Opciones{
		Aplicar: true, Perfil: PerfilPorDefecto(), Runner: c, Sonda: SondaVacia{}, Salida: &Registro{}, Ahora: time.Now,
	})
	if err != nil || inf.Codigo != "en_curso" || c.n != 0 {
		t.Fatalf("%+v n=%d %v", inf, c.n, err)
	}
	// El latido tampoco pisa: devuelve lo guardado.
	if _, err := Latido(context.Background(), st, gitFalso{[]string{"x.ts"}}, "123", time.Now()); err != nil {
		t.Fatal(err)
	}
	if w, _ := st.Load("123"); w.Actividad != nil {
		t.Fatal("el latido escribió con el WI trabado")
	}
}

func TestGateColgadoSeCortaConTodoSuArbol(t *testing.T) {
	cfg := `{"gates":[{"name":"test","cmd":"sleep 30 & echo $! > hijo.pid; wait","hard":true,"timeoutSec":1}],
	 "e2e":{"artifact":"e2e/report.json"},"prTarget":"qa","branchPrefix":"feature/","tracker":{"provider":"azure","repo":"r"}}`
	st, wt := preparar(t, cfg)
	t0 := time.Now()
	inf, err := Publicar(context.Background(), st, "123", Opciones{
		Aplicar: true, Perfil: PerfilPorDefecto(), Runner: Bash{}, Sonda: SondaVacia{}, Salida: &Registro{}, Ahora: time.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Since(t0); d > 10*time.Second {
		t.Fatalf("tardó %s: el plazo no cortó", d)
	}
	if inf.Publico || inf.Gates[0].Exit != 124 {
		t.Fatalf("%+v", inf.Gates)
	}
	pid, _ := os.ReadFile(filepath.Join(wt, "hijo.pid"))
	time.Sleep(200 * time.Millisecond)
	if err := exec_kill0(string(pid)); err == nil {
		t.Fatal("el sleep hijo sigue vivo")
	}
}

func exec_kill0(pid string) error {
	n, err := strconv.Atoi(strings.TrimSpace(pid))
	if err != nil {
		return err
	}
	return syscall.Kill(n, 0)
}

func TestPlaywrightSalteadoOInestableNoEsVerde(t *testing.T) {
	d := t.TempDir()
	casos := map[string]bool{
		`{"stats":{"expected":3,"skipped":0,"unexpected":0,"flaky":0}}`: true,
		`{"stats":{"expected":3,"skipped":1,"unexpected":0,"flaky":0}}`: false,
		`{"stats":{"expected":3,"skipped":0,"unexpected":0,"flaky":1}}`: false,
		`{"stats":{"expected":2,"skipped":0,"unexpected":1,"flaky":0}}`: false,
		`{"stats":{"expected":0,"skipped":0,"unexpected":0,"flaky":0}}`: false,
		`{"ok":true,"pasos":[{"ok":true}]}`:                             true,
		`{"ok":true,"pasos":[{"ok":true},{"ok":false}]}`:                false,
	}
	i := 0
	for cuerpo, quiero := range casos {
		p := filepath.Join(d, strconv.Itoa(i)+".json")
		i++
		_ = os.WriteFile(p, []byte(cuerpo), 0o644)
		if got := journeyPasoOk(p); got != quiero {
			t.Errorf("%s: %v, quería %v", cuerpo, got, quiero)
		}
	}
}

func TestCmdConEvidenciaSoloTocaPlaywright(t *testing.T) {
	got := CmdConEvidencia("npx playwright test e2e/alta.spec.ts", "e2e/evidencia/alta/report.json")
	quiero := `mkdir -p "$(dirname 'e2e/evidencia/alta/report.json')" && PLAYWRIGHT_JSON_OUTPUT_FILE='e2e/evidencia/alta/report.json' npx playwright test e2e/alta.spec.ts --reporter=json`
	if got != quiero {
		t.Fatalf("\n%s\n%s", got, quiero)
	}
	if c := "dotnet test Api.E2E"; CmdConEvidencia(c, "x.json") != c {
		t.Fatal("dotnet no se toca")
	}
}

func TestDockerRunNoLlevaEnvFileNiSocket(t *testing.T) {
	s, err := PlanCaja("77", "/w/us-77", PerfilPorDefecto())
	if err != nil {
		t.Fatal(err)
	}
	s.Imagen, s.Red = "img:1", "br-agentbox"
	args := strings.Join(ArgsDockerRun(s), " ")
	for _, malo := range []string{"--env-file", "docker.sock", "supervisor.env", "--privileged", "GH_TOKEN"} {
		if strings.Contains(args, malo) {
			t.Fatalf("docker run trae %s: %s", malo, args)
		}
	}
	for _, bueno := range []string{"--name box-77", "--label agent-box.wi=77", "--memory 1200m --memory-swap 1200m", "--runtime runsc", "--init", "-v /w/us-77:/w/us-77", "--entrypoint sleep img:1 infinity"} {
		if !strings.Contains(args, bueno) {
			t.Fatalf("falta %q en: %s", bueno, args)
		}
	}
}

func TestSalidaAzUsaElEnvDelSupervisorYArmaLaURL(t *testing.T) {
	envf := filepath.Join(t.TempDir(), "supervisor.env")
	_ = os.WriteFile(envf, []byte("# pat\nAZURE_DEVOPS_EXT_PAT=secreto\n\n"), 0o600)
	var visto [][]string
	var envVisto []string
	s := SalidaAz{EnvFile: envf, Exec: func(_ context.Context, env, argv []string) (string, error) {
		visto, envVisto = append(visto, argv), env
		return `{"pullRequestId": 812, "repository": {"webUrl": "https://dev.azure.com/o/p/_git/r"}}`, nil
	}}
	salida, err := s.AbrirPR(context.Background(), []string{"az", "repos", "pr", "create"})
	if url := (azure{}).URL(salida); err != nil || url != "https://dev.azure.com/o/p/_git/r/pullrequest/812" {
		t.Fatalf("%q %v", url, err)
	}
	if len(envVisto) != 1 || envVisto[0] != "AZURE_DEVOPS_EXT_PAT=secreto" {
		t.Fatalf("env %v", envVisto)
	}
	if _, err := (SalidaAz{EnvFile: filepath.Join(t.TempDir(), "no")}).AbrirPR(context.Background(), []string{"az", "repos", "pr"}); err == nil {
		t.Fatal("sin env del supervisor tiene que fallar")
	}
}

func TestGitHubArmaComentarioYPRConGh(t *testing.T) {
	w := WI{ID: "42", PRTarget: "main", Tracker: Tracker{Provider: "github", Repo: "mi-org/mi-repo"}}
	p, ok := ProveedorDe(w.Tracker)
	if !ok || p.Falta(w) != "" {
		t.Fatal("github no quedó registrado")
	}
	pr := strings.Join(p.PR(w, "feature/42", "Arregla X", "cuerpo"), " ")
	for _, parte := range []string{"gh pr create", "--repo mi-org/mi-repo", "--head feature/42", "--base main", "--title Arregla X", "Refs #42"} {
		if !strings.Contains(pr, parte) {
			t.Fatalf("falta %q en: %s", parte, pr)
		}
	}
	if c := strings.Join(p.Comentario(w, "<b>x</b>"), " "); !strings.HasPrefix(c, "gh issue comment 42 --repo mi-org/mi-repo --body") {
		t.Fatal(c)
	}
	if u := p.URL("Creating pull request...\nhttps://github.com/mi-org/mi-repo/pull/7\n"); u != "https://github.com/mi-org/mi-repo/pull/7" {
		t.Fatal(u)
	}
	if (github{}).Falta(WI{Tracker: Tracker{Repo: "sin-dueño"}}) == "" {
		t.Fatal("un repo sin dueño tiene que faltar")
	}
}

func TestTrackerDesconocidoNoCorreGates(t *testing.T) {
	cfg := strings.Replace(cfgBueno, `"provider": "azure"`, `"provider": "jira"`, 1)
	st, _ := preparar(t, cfg)
	c := &cuenta{}
	inf, err := Publicar(context.Background(), st, "123", Opciones{Aplicar: true, Perfil: PerfilPorDefecto(), Runner: c, Sonda: SondaVacia{}, Salida: &Registro{}})
	if err != nil || inf.Codigo != "tracker_desconocido" || c.n != 0 {
		t.Fatalf("%+v n=%d %v", inf, c.n, err)
	}
}

func TestSinTrackerLosGatesDecidenYNoSeEscribeAfuera(t *testing.T) {
	cfg := strings.Replace(cfgBueno, `"provider": "azure"`, `"provider": "ninguno"`, 1)
	st, _ := preparar(t, cfg)
	reg := &Registro{}
	run := &cuenta{fn: func(dir string) {
		os.MkdirAll(filepath.Join(dir, "e2e"), 0o755)
		os.WriteFile(filepath.Join(dir, "e2e", "report.json"), []byte("ok"), 0o644)
	}}
	inf, err := Publicar(context.Background(), st, "123", Opciones{Aplicar: true, Perfil: PerfilPorDefecto(), Runner: run, Sonda: SondaVacia{}, Salida: reg})
	if err != nil || !inf.Publico || len(reg.Llamadas) != 0 {
		t.Fatalf("%+v llamadas=%d %v", inf, len(reg.Llamadas), err)
	}
}

type gitConHead struct {
	gitFalso
	head string
}

func (g gitConHead) Head(context.Context, string) string { return g.head }

func TestReglasFrenanAntesDeLosGates(t *testing.T) {
	cfg := strings.Replace(cfgBueno, `"prTarget"`, `"reglas": {"testsConCodigo": true, "veredicto": true}, "prTarget"`, 1)
	st, wt := preparar(t, cfg)
	ctx := context.Background()
	tocar(t, wt, "src/a.ts", time.Now())
	c := &cuenta{fn: func(dir string) {
		os.MkdirAll(filepath.Join(dir, "e2e"), 0o755)
		os.WriteFile(filepath.Join(dir, "e2e", "report.json"), []byte("ok"), 0o644)
	}}
	pub := func(g Git) Informe {
		inf, err := Publicar(ctx, st, "123", Opciones{Aplicar: true, Perfil: PerfilPorDefecto(), Runner: c, Sonda: SondaVacia{}, Salida: &Registro{}, Git: g, Ahora: time.Now})
		if err != nil {
			t.Fatal(err)
		}
		return inf
	}
	soloCodigo := gitConHead{gitFalso{[]string{"src/a.ts"}}, "aaaa1111"}
	if inf := pub(soloCodigo); inf.Codigo != "sin_tests" || c.n != 0 {
		t.Fatalf("código sin tests: %+v n=%d", inf, c.n)
	}
	conTests := gitConHead{gitFalso{[]string{"src/a.ts", "src/a.spec.ts"}}, "aaaa1111"}
	if inf := pub(conTests); inf.Codigo != "sin_veredicto" || c.n != 0 {
		t.Fatalf("sin revisión: %+v", inf)
	}
	if _, err := AnotarVeredicto(ctx, st, conTests, "123", false, "revisora", "falta un caso", time.Now()); err != nil {
		t.Fatal(err)
	}
	if inf := pub(conTests); inf.Codigo != "veredicto_rechazado" {
		t.Fatalf("rechazado: %+v", inf)
	}
	if _, err := AnotarVeredicto(ctx, st, conTests, "123", true, "revisora", "", time.Now()); err != nil {
		t.Fatal(err)
	}
	otroCommit := gitConHead{conTests.gitFalso, "bbbb2222"}
	if inf := pub(otroCommit); inf.Codigo != "veredicto_viejo" || c.n != 0 {
		t.Fatalf("un commit nuevo vence la revisión: %+v", inf)
	}
	if inf := pub(conTests); !inf.Publico || c.n == 0 {
		t.Fatalf("con todo en regla tiene que publicar: %+v", inf)
	}
	if _, err := AnotarVeredicto(ctx, st, conTests, "123", true, "", "", time.Now()); err == nil {
		t.Fatal("un veredicto sin autor no vale")
	}
}
