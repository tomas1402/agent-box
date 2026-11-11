package reloj

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const cfgBueno = `{
  "gates": [
    {"name": "test", "cmd": "mkdir -p e2e && touch e2e/report.json", "hard": true},
    {"name": "lint", "cmd": "echo no-cuento", "hard": false}
  ],
  "e2e": {"artifact": "e2e/report.json"},
  "prTarget": "main",
  "branchPrefix": "feature/",
  "prTitlePrefix": "[demo] ",
  "tracker": {"provider": "azure", "repo": "repo-demo"}
}`

func ahoraFijo() time.Time { return time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC) }

type memFake struct {
	ups, downs []int
	failUp     bool
}

func (m *memFake) Aplicar(_ string, maxMB, _ int) (bool, error) {
	if maxMB > 2000 {
		if m.failUp {
			return false, errors.New("no sube")
		}
		m.ups = append(m.ups, maxMB)
		return false, nil
	}
	m.downs = append(m.downs, maxMB)
	return false, nil
}

type cuenta struct {
	n    int
	cmds []string
	exit int
	cola string
	fn   func(dir string)
}

func (c *cuenta) Run(_ context.Context, dir, nombre, cmd string) (int, string, error) {
	c.n++
	c.cmds = append(c.cmds, nombre+" "+cmd)
	if c.fn != nil {
		c.fn(dir)
	}
	return c.exit, c.cola, nil
}

func preparar(t *testing.T, cfg string) (Store, string) {
	t.Helper()
	raiz := t.TempDir()
	wt := filepath.Join(raiz, "wt")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	conf := filepath.Join(raiz, "harness.json")
	if err := os.WriteFile(conf, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	st := Store{Dir: filepath.Join(raiz, "estado")}
	if _, err := Alta(st, "123", wt, conf, "", PerfilPorDefecto(), ahoraFijo()); err != nil {
		t.Fatal(err)
	}
	return st, wt
}

func TestAltaRechazaIDYConfigConComentarios(t *testing.T) {
	st := Store{Dir: t.TempDir()}
	if _, err := Alta(st, "../x", "/tmp/wt", "no.json", "", PerfilPorDefecto(), ahoraFijo()); err == nil {
		t.Fatal("id con barra")
	}
	dir := t.TempDir()
	conf := filepath.Join(dir, "c.json")
	os.WriteFile(conf, []byte("// no\n{}"), 0o644)
	wt := filepath.Join(dir, "wt")
	os.MkdirAll(wt, 0o755)
	_, err := Alta(Store{Dir: dir}, "1", wt, conf, "", PerfilPorDefecto(), ahoraFijo())
	if err == nil || !strings.Contains(err.Error(), "config_no_json") {
		t.Fatalf("config: %v", err)
	}
}

func TestPlanCajaNoMontaSecretos(t *testing.T) {
	s, err := PlanCaja("123", "/home/agent/workspace/wt", PerfilPorDefecto())
	if err != nil {
		t.Fatal(err)
	}
	if s.Runtime != "runsc" || !s.Init || s.MemoryMaxMB != 1200 || s.MemoryHighMB != 800 {
		t.Fatalf("spec %+v", s)
	}
	if err := SpecLimpia(s); err != nil {
		t.Fatal(err)
	}
	sucia := s
	sucia.Montajes = append(sucia.Montajes, "/var/run/docker.sock:/var/run/docker.sock")
	if err := SpecLimpia(sucia); err == nil {
		t.Fatal("docker.sock")
	}
	sucia = s
	sucia.Env = []string{"GH_TOKEN=x"}
	if err := SpecLimpia(sucia); err == nil {
		t.Fatal("token")
	}
	if HighAplicadoPorDocker() {
		t.Fatal("docker update no aplica memory.high")
	}
	args := ArgsDockerUpdate("box-123", 5120)
	if strings.Join(args, " ") != "docker update -m 5120m --memory-swap 5120m box-123" {
		t.Fatalf("args %v", args)
	}
}

func TestCabeBurstReemplazaElTechoDeEstaCaja(t *testing.T) {
	p := PerfilPorDefecto()
	cuatro := []CajaViva{{"123", 1200}, {"a", 1200}, {"b", 1200}, {"c", 1200}}
	if !CabeBurst(p, cuatro, "123") {
		t.Fatal("4 cajas tienen que entrar")
	}
	var ocho []CajaViva
	for _, id := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		ocho = append(ocho, CajaViva{id, 1200})
	}
	if CabeBurst(p, ocho, "123") {
		t.Fatal("8 ajenas más el burst no entran en 12 GB")
	}
	if CabeBurst(p, nil, "123") != true {
		t.Fatal("una sola caja entra")
	}
}

func TestPublicarSecoNoCorreNiEscribe(t *testing.T) {
	st, _ := preparar(t, cfgBueno)
	run := &cuenta{exit: 1}
	inf, err := Publicar(context.Background(), st, "123", Opciones{Aplicar: false, Runner: run, Perfil: PerfilPorDefecto()})
	if err != nil || inf.Codigo != "seco" || run.n != 0 {
		t.Fatalf("inf %+v err %v n %d", inf, err, run.n)
	}
	w, _ := st.Load("123")
	if w.Fase != Admitido {
		t.Fatalf("fase %s", w.Fase)
	}
}

func TestPublicarFrenaYBajaLaRAMSinTocarAzure(t *testing.T) {
	st, _ := preparar(t, cfgBueno)
	mem := &memFake{}
	run := &cuenta{exit: 1, cola: "AZURE_PAT=secret"}
	reg := &Registro{}
	inf, err := Publicar(context.Background(), st, "123", Opciones{
		Aplicar: true, Perfil: PerfilPorDefecto(), Runner: run, Mem: mem, Sonda: SondaVacia{}, Salida: reg,
	})
	if err != nil {
		t.Fatal(err)
	}
	if inf.Codigo != "gates_rojos" || inf.Publico || inf.Fase != Frenado {
		t.Fatalf("%+v", inf)
	}
	if len(reg.Llamadas) != 0 {
		t.Fatalf("az fue llamado: %v", reg.Llamadas)
	}
	if len(mem.ups) != 1 || mem.ups[0] != 5120 || len(mem.downs) != 1 || mem.downs[0] != 1200 {
		t.Fatalf("mem ups %v downs %v", mem.ups, mem.downs)
	}
	if inf.Memoria.HighAplicado {
		t.Fatal("no hay que marcar high como aplicado")
	}
	w, _ := st.Load("123")
	if w.FallosSeguidos != 1 || w.UltimoGate != "test" {
		t.Fatalf("%+v", w)
	}
}

func TestPasoVerdeNoAlcanzaParaPublicar(t *testing.T) {
	st, _ := preparar(t, cfgBueno)
	if _, err := Paso(context.Background(), st, "123", "test", &cuenta{exit: 0}, nil, ahoraFijo()); err != nil {
		t.Fatal(err)
	}
	w, _ := st.Load("123")
	if w.Fase != Trabajando || w.UltimoPaso != "test" {
		t.Fatalf("paso %+v", w)
	}
	reg := &Registro{}
	inf, err := Publicar(context.Background(), st, "123", Opciones{
		Aplicar: true, Perfil: PerfilPorDefecto(), Runner: &cuenta{exit: 1}, Sonda: SondaVacia{}, Salida: reg,
	})
	if err != nil || inf.Publico || len(reg.Llamadas) != 0 {
		t.Fatalf("el paso no cuenta: %+v %v", inf, err)
	}
}

func TestPinNoSeReleeDelWorktree(t *testing.T) {
	st, wt := preparar(t, cfgBueno)
	// El agente cambia el harness después del alta.
	os.WriteFile(filepath.Join(filepath.Dir(wt), "harness.json"), []byte(`{"gates":[{"name":"test","cmd":"echo MUTADO","hard":true}]}`), 0o644)
	run := &cuenta{exit: 1}
	_, err := Publicar(context.Background(), st, "123", Opciones{
		Aplicar: true, Perfil: PerfilPorDefecto(), Runner: run, Sonda: SondaVacia{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(run.cmds) != 1 || !strings.Contains(run.cmds[0], "touch e2e/report.json") || strings.Contains(run.cmds[0], "MUTADO") {
		t.Fatalf("corrió el config vivo: %v", run.cmds)
	}
	if strings.Contains(run.cmds[0], "no-cuento") {
		t.Fatal("corrió un gate blando")
	}
}

func TestE2EViejoNoPublicaYElFrescoSi(t *testing.T) {
	st, wt := preparar(t, cfgBueno)
	os.MkdirAll(filepath.Join(wt, "e2e"), 0o755)
	viejo := filepath.Join(wt, "e2e", "report.json")
	os.WriteFile(viejo, []byte("x"), 0o644)
	os.Chtimes(viejo, time.Now().Add(-time.Hour), time.Now().Add(-time.Hour))
	reg := &Registro{}
	inf, err := Publicar(context.Background(), st, "123", Opciones{
		Aplicar: true, Perfil: PerfilPorDefecto(), Runner: &cuenta{exit: 0}, Sonda: SondaVacia{}, Salida: reg,
	})
	if err != nil || inf.Codigo != "e2e_viejo" || len(reg.Llamadas) != 0 {
		t.Fatalf("%+v %v llamadas %d", inf, err, len(reg.Llamadas))
	}

	st2, _ := preparar(t, cfgBueno)
	reg2 := &Registro{}
	inf, err = Publicar(context.Background(), st2, "123", Opciones{
		Aplicar: true, Perfil: PerfilPorDefecto(), Runner: Bash{}, Mem: &memFake{}, Sonda: SondaVacia{}, Salida: reg2,
	})
	if err != nil || !inf.Publico || inf.Fase != Listo {
		t.Fatalf("fresco %+v %v", inf, err)
	}
	if len(reg2.Llamadas) != 2 {
		t.Fatalf("llamadas %v", reg2.Llamadas)
	}
	disc := strings.Join(reg2.Llamadas[0], " ")
	if !strings.Contains(disc, "az boards work-item update") || strings.Contains(disc, "secret") {
		t.Fatalf("comentario %s", disc)
	}
	pr := strings.Join(reg2.Llamadas[1], " ")
	if !strings.Contains(pr, "az repos pr create") || !strings.Contains(pr, "--target-branch main") || !strings.Contains(pr, "--work-items 123") {
		t.Fatalf("pr %s", pr)
	}
	w, _ := st2.Load("123")
	if w.Fase != Listo {
		t.Fatal(w.Fase)
	}
	otra := &cuenta{exit: 1}
	inf, err = Publicar(context.Background(), st2, "123", Opciones{
		Aplicar: true, Perfil: PerfilPorDefecto(), Runner: otra, Sonda: SondaVacia{}, Salida: &Registro{},
	})
	if err != nil || inf.Codigo != "ya_listo" || otra.n != 0 {
		t.Fatalf("segundo publicar %+v n %d", inf, otra.n)
	}
}

func TestColaConSecretoNoEntraAlWI(t *testing.T) {
	st, wt := preparar(t, cfgBueno)
	run := &cuenta{exit: 0, cola: "AZURE_PAT=secret", fn: func(dir string) {
		os.MkdirAll(filepath.Join(dir, "e2e"), 0o755)
		os.WriteFile(filepath.Join(dir, "e2e", "report.json"), []byte("ok"), 0o644)
	}}
	_ = wt
	reg := &Registro{}
	inf, err := Publicar(context.Background(), st, "123", Opciones{
		Aplicar: true, Perfil: PerfilPorDefecto(), Runner: run, Sonda: SondaVacia{}, Salida: reg,
	})
	if err != nil || !inf.Publico {
		t.Fatalf("%+v %v", inf, err)
	}
	todo := strings.Join(reg.Llamadas[0], " ") + strings.Join(reg.Llamadas[1], " ")
	if strings.Contains(todo, "secret") || strings.Contains(todo, "AZURE_PAT") {
		t.Fatalf("el comentario se llevó la cola: %s", todo)
	}
}

func TestPreexistingMismoExitPasaYOtroNo(t *testing.T) {
	raiz := t.TempDir()
	wt := filepath.Join(raiz, "wt")
	os.MkdirAll(wt, 0o755)
	conf := filepath.Join(raiz, "h.json")
	os.WriteFile(conf, []byte(cfgBueno), 0o644)
	base := filepath.Join(raiz, "base.json")
	os.WriteFile(base, []byte(`{"test": 2}`), 0o644)
	st := Store{Dir: filepath.Join(raiz, "estado")}
	if _, err := Alta(st, "9", wt, conf, base, PerfilPorDefecto(), ahoraFijo()); err != nil {
		t.Fatal(err)
	}
	reg := &Registro{}
	inf, err := Publicar(context.Background(), st, "9", Opciones{
		Aplicar: true, Perfil: PerfilPorDefecto(),
		Runner: &cuenta{exit: 1, fn: func(dir string) {
			os.MkdirAll(filepath.Join(dir, "e2e"), 0o755)
			os.WriteFile(filepath.Join(dir, "e2e", "report.json"), []byte("x"), 0o644)
		}},
		Sonda: SondaVacia{}, Salida: reg,
	})
	if err != nil || inf.Codigo != "gates_rojos" || len(reg.Llamadas) != 0 {
		t.Fatalf("exit distinto: %+v", inf)
	}

	st2 := Store{Dir: filepath.Join(raiz, "estado2")}
	if _, err := Alta(st2, "9", wt, conf, base, PerfilPorDefecto(), ahoraFijo()); err != nil {
		t.Fatal(err)
	}
	reg2 := &Registro{}
	inf, err = Publicar(context.Background(), st2, "9", Opciones{
		Aplicar: true, Perfil: PerfilPorDefecto(),
		Runner: &cuenta{exit: 2, fn: func(dir string) {
			os.MkdirAll(filepath.Join(dir, "e2e"), 0o755)
			os.WriteFile(filepath.Join(dir, "e2e", "report.json"), []byte("x"), 0o644)
		}},
		Sonda: SondaVacia{}, Salida: reg2,
	})
	if err != nil || !inf.Publico || !inf.Gates[0].Preexisting {
		t.Fatalf("mismo exit: %+v %v", inf, err)
	}
}

func TestTresFallosEscalanYElCuartoNoCorre(t *testing.T) {
	st, _ := preparar(t, cfgBueno)
	run := &cuenta{exit: 1}
	opt := Opciones{Aplicar: true, Perfil: PerfilPorDefecto(), Runner: run, Sonda: SondaVacia{}}
	for i := 0; i < 3; i++ {
		inf, err := Publicar(context.Background(), st, "123", opt)
		if err != nil {
			t.Fatal(err)
		}
		if i < 2 && inf.Fase != Frenado {
			t.Fatalf("vuelta %d fase %s", i, inf.Fase)
		}
	}
	w, _ := st.Load("123")
	if w.Fase != Escalado || run.n != 3 {
		t.Fatalf("fase %s n %d", w.Fase, run.n)
	}
	inf, err := Publicar(context.Background(), st, "123", opt)
	if err != nil || inf.Codigo != "escalado" || run.n != 3 {
		t.Fatalf("cuarto %+v n %d", inf, run.n)
	}
	if _, err := Destrabar(st, "123", ahoraFijo()); err != nil {
		t.Fatal(err)
	}
	inf, err = Publicar(context.Background(), st, "123", opt)
	if err != nil || inf.Codigo != "gates_rojos" || run.n != 4 {
		t.Fatalf("despues de destrabar %+v n %d err %v", inf, run.n, err)
	}
}

func TestCredencialYPresupuestoNoCorrenGates(t *testing.T) {
	st, _ := preparar(t, cfgBueno)
	run := &cuenta{exit: 0}
	inf, err := Publicar(context.Background(), st, "123", Opciones{
		Aplicar: true, Perfil: PerfilPorDefecto(), Runner: run,
		Sonda: sondaEnv{env: []string{"AZURE_DEVOPS_EXT_PAT=x"}},
	})
	if err != nil || inf.Codigo != "credencial_en_la_caja" || run.n != 0 {
		t.Fatalf("%+v n %d", inf, run.n)
	}
	var vivas []CajaViva
	for _, id := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		vivas = append(vivas, CajaViva{id, 1200})
	}
	inf, err = Publicar(context.Background(), st, "123", Opciones{
		Aplicar: true, Perfil: PerfilPorDefecto(), Vivas: vivas, Runner: run, Sonda: SondaVacia{},
	})
	if err != nil || inf.Codigo != "presupuesto" || run.n != 0 {
		t.Fatalf("presupuesto %+v n %d", inf, run.n)
	}
}

func TestPRQueFallaNoDejaElWIListo(t *testing.T) {
	st, _ := preparar(t, cfgBueno)
	reg := &Registro{FallarEn: "pr"}
	run := &cuenta{exit: 0, fn: func(dir string) {
		os.MkdirAll(filepath.Join(dir, "e2e"), 0o755)
		os.WriteFile(filepath.Join(dir, "e2e", "report.json"), []byte("x"), 0o644)
	}}
	inf, err := Publicar(context.Background(), st, "123", Opciones{
		Aplicar: true, Perfil: PerfilPorDefecto(), Runner: run, Sonda: SondaVacia{}, Salida: reg,
	})
	if err != nil || inf.Publico || inf.Codigo != "pr" || inf.Fase == Listo {
		t.Fatalf("%+v", inf)
	}
	if len(reg.Llamadas) != 2 {
		t.Fatalf("tendría que haber comentado y fallado el PR: %d", len(reg.Llamadas))
	}
}

func TestYaListoNoSePisa(t *testing.T) {
	st, wt := preparar(t, cfgBueno)
	w, _ := st.Load("123")
	w.Fase = Listo
	st.Save(w)
	_, err := Alta(st, "123", wt, filepath.Join(filepath.Dir(wt), "harness.json"), "", PerfilPorDefecto(), ahoraFijo())
	if err == nil || !strings.Contains(err.Error(), "ya_listo") {
		t.Fatal(err)
	}
}

type sondaEnv struct{ env []string }

func (s sondaEnv) Env(string) ([]string, error)      { return s.env, nil }
func (s sondaEnv) Montajes(string) ([]string, error) { return nil, nil }
