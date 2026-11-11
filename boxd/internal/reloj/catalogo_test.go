package reloj

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMatchearPineaHeadlessYDejaHueco(t *testing.T) {
	viajes := []Viaje{
		{ID: "admin", AC: []string{"AC1"}, Modo: "headless", Cmd: "npx playwright test e2e/admin.spec.ts"},
		{ID: "login", AC: []string{"AC2"}, Modo: "browse", Cmd: "npx playwright test e2e/login.spec.ts"},
	}
	pin, esp, huecos := Matchear([]string{"AC1", "AC2", "AC3"}, viajes)
	if len(pin) != 1 || pin[0].ID != "admin" {
		t.Fatalf("pin %v", pin)
	}
	if len(esp) != 1 || esp[0] != "AC2" || len(huecos) != 1 || huecos[0] != "AC3" {
		t.Fatalf("esp %v huecos %v", esp, huecos)
	}
}

func TestCmdDeViajeNoAceptaTouchNiExit(t *testing.T) {
	if CmdPermitido("touch e2e/report.json") || CmdPermitido("exit 0") || CmdPermitido("npm run e2e-evil") {
		t.Fatal("comando flojo aceptado")
	}
	if !CmdPermitido("npx playwright test e2e/admin.spec.ts") || !CmdPermitido("npm run e2e -- admin") || !CmdPermitido("dotnet test e2e") {
		t.Fatal("runner real rechazado")
	}
}

func TestHuecoFrenaYLaPropuestaEnElCatalogoPublica(t *testing.T) {
	st, wt := preparar(t, cfgBueno)
	cat := filepath.Join(wt, "e2e", "catalogo.json")
	os.MkdirAll(filepath.Dir(cat), 0o755)
	os.WriteFile(cat, []byte(`{"viajes":[{"id":"admin","ac":["AC1"],"modo":"headless","cmd":"npx playwright test e2e/admin.spec.ts","artefacto":"e2e/evidencia/admin/journey.json"}]}`), 0o644)
	if err := PinCatalogo(st, "123", cat, []string{"AC1", "AC3"}, ahoraFijo()); err != nil {
		t.Fatal(err)
	}
	w, _ := st.Load("123")
	if len(w.Huecos) != 1 || w.Huecos[0] != "AC3" || len(w.Viajes) != 1 {
		t.Fatalf("%+v", w)
	}
	// El agente reescribe el catálogo. El viaje pineado no cambia.
	os.WriteFile(cat, []byte(`{"viajes":[]}`), 0o644)
	run := &cuenta{exit: 0, fn: func(dir string) {
		os.MkdirAll(filepath.Join(dir, "e2e", "evidencia", "admin"), 0o755)
		os.WriteFile(filepath.Join(dir, "e2e", "report.json"), []byte("x"), 0o644)
		os.WriteFile(filepath.Join(dir, "e2e", "evidencia", "admin", "journey.json"), []byte(`{"ok":true,"pasos":[{"ok":true}]}`), 0o644)
	}}
	reg := &Registro{}
	inf, err := Publicar(context.Background(), st, "123", Opciones{
		Aplicar: true, Perfil: PerfilPorDefecto(), Runner: run, Sonda: SondaVacia{}, Salida: reg,
	})
	if err != nil || inf.Codigo != "ac_sin_viaje" || len(reg.Llamadas) != 0 {
		t.Fatalf("%+v %v", inf, err)
	}
	if len(run.cmds) == 0 || !strings.Contains(strings.Join(run.cmds, "\n"), "viaje:admin") {
		t.Fatalf("no corrió el viaje pineado: %v", run.cmds)
	}

	prop := filepath.Join(wt, "e2e", "propuestas", "123.json")
	os.MkdirAll(filepath.Dir(prop), 0o755)
	os.WriteFile(prop, []byte(`{"id":"nuevo","ac":["AC3"],"modo":"headless","cmd":"npx playwright test e2e/nuevo.spec.ts","artefacto":"e2e/evidencia/nuevo/journey.json"}`), 0o644)
	run.fn = func(dir string) {
		os.MkdirAll(filepath.Join(dir, "e2e"), 0o755)
		os.WriteFile(filepath.Join(dir, "e2e", "report.json"), []byte("x"), 0o644)
		os.MkdirAll(filepath.Join(dir, "e2e", "evidencia", "nuevo"), 0o755)
		os.WriteFile(filepath.Join(dir, "e2e", "evidencia", "nuevo", "journey.json"), []byte(`{"ok":true,"pasos":[{"ok":true}]}`), 0o644)
		os.MkdirAll(filepath.Join(dir, "e2e", "evidencia", "admin"), 0o755)
		os.WriteFile(filepath.Join(dir, "e2e", "evidencia", "admin", "journey.json"), []byte(`{"ok":true,"pasos":[{"ok":true}]}`), 0o644)
	}
	inf, err = Publicar(context.Background(), st, "123", Opciones{
		Aplicar: true, Perfil: PerfilPorDefecto(), Runner: run, Sonda: SondaVacia{}, Salida: reg,
	})
	if err != nil || inf.Codigo != "catalogo_sin_propuesta" || inf.Publico {
		t.Fatalf("sin copiar al catalogo: %+v %v", inf, err)
	}
	os.WriteFile(cat, []byte(`{"viajes":[
	  {"id":"admin","ac":["AC1"],"modo":"headless","cmd":"npx playwright test e2e/admin.spec.ts","artefacto":"e2e/evidencia/admin/journey.json"},
	  {"id":"nuevo","ac":["AC3"],"modo":"headless","cmd":"npx playwright test e2e/nuevo.spec.ts","artefacto":"e2e/evidencia/nuevo/journey.json"}
	]}`), 0o644)
	reg.Llamadas = nil
	inf, err = Publicar(context.Background(), st, "123", Opciones{
		Aplicar: true, Perfil: PerfilPorDefecto(), Runner: run, Sonda: SondaVacia{}, Salida: reg,
	})
	if err != nil || !inf.Publico || len(reg.Llamadas) != 2 {
		t.Fatalf("con catalogo: %+v %v llamadas %d", inf, err, len(reg.Llamadas))
	}
}

func TestReemplazoPerdonaElViajeViejoSiCubreLosMismosAC(t *testing.T) {
	st, wt := preparar(t, cfgBueno)
	cat := filepath.Join(wt, "e2e", "catalogo.json")
	os.MkdirAll(filepath.Dir(cat), 0o755)
	cmdViejo := "npx playwright test e2e/admin.spec.ts"
	os.WriteFile(cat, []byte(`{"viajes":[{"id":"admin","ac":["AC1"],"modo":"headless","cmd":"`+cmdViejo+`","artefacto":"e2e/evidencia/admin/journey.json"}]}`), 0o644)
	if err := PinCatalogo(st, "123", cat, []string{"AC1"}, ahoraFijo()); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(wt, "e2e", "propuestas"), 0o755)
	os.WriteFile(filepath.Join(wt, "e2e", "propuestas", "123.json"), []byte(`{"id":"admin-v2","ac":["AC1"],"reemplaza":"admin","cmd":"npx playwright test e2e/admin-v2.spec.ts","artefacto":"e2e/evidencia/admin-v2/journey.json"}`), 0o644)
	os.WriteFile(cat, []byte(`{"viajes":[
	  {"id":"admin","ac":["AC1"],"modo":"headless","cmd":"`+cmdViejo+`","artefacto":"e2e/evidencia/admin/journey.json"},
	  {"id":"admin-v2","ac":["AC1"],"cmd":"npx playwright test e2e/admin-v2.spec.ts","artefacto":"e2e/evidencia/admin-v2/journey.json","reemplaza":"admin"}
	]}`), 0o644)
	run := &cuenta{fn: func(dir string) {
		os.MkdirAll(filepath.Join(dir, "e2e"), 0o755)
		os.WriteFile(filepath.Join(dir, "e2e", "report.json"), []byte("x"), 0o644)
		os.MkdirAll(filepath.Join(dir, "e2e", "evidencia", "admin-v2"), 0o755)
		os.WriteFile(filepath.Join(dir, "e2e", "evidencia", "admin-v2", "journey.json"), []byte(`{"ok":true,"pasos":[{"ok":true}]}`), 0o644)
	}}
	run.exit = 0
	// el viaje viejo falla; la propuesta pasa. cuenta no distingue por nombre:
	// se sobreescribe Run abajo.
	distinto := runnerPorNombre{
		porDefecto: 1,
		ok:         map[string]bool{"propuesta:admin-v2": true, "test": true},
		fn:         run.fn,
	}
	reg := &Registro{}
	inf, err := Publicar(context.Background(), st, "123", Opciones{
		Aplicar: true, Perfil: PerfilPorDefecto(), Runner: distinto, Sonda: SondaVacia{}, Salida: reg,
	})
	if err != nil || !inf.Publico {
		t.Fatalf("%+v %v", inf, err)
	}
	vio := false
	for _, g := range inf.Gates {
		if g.Nombre == "viaje:admin" && g.Reemplazado && g.Exit != 0 {
			vio = true
		}
	}
	if !vio {
		t.Fatalf("no perdonó el viaje viejo: %+v", inf.Gates)
	}
}

func TestBrowseNoPublicaYNoEscala(t *testing.T) {
	st, wt := preparar(t, cfgBueno)
	cat := filepath.Join(wt, "e2e", "catalogo.json")
	os.MkdirAll(filepath.Dir(cat), 0o755)
	os.WriteFile(cat, []byte(`{"viajes":[{"id":"login","ac":["AC2"],"modo":"browse","cmd":"npx playwright test e2e/login.spec.ts","artefacto":"e2e/evidencia/login/journey.json"}]}`), 0o644)
	if err := PinCatalogo(st, "123", cat, []string{"AC2"}, ahoraFijo()); err != nil {
		t.Fatal(err)
	}
	run := &cuenta{exit: 0, fn: func(dir string) {
		os.MkdirAll(filepath.Join(dir, "e2e"), 0o755)
		os.WriteFile(filepath.Join(dir, "e2e", "report.json"), []byte("x"), 0o644)
	}}
	for i := 0; i < 3; i++ {
		inf, err := Publicar(context.Background(), st, "123", Opciones{
			Aplicar: true, Perfil: PerfilPorDefecto(), Runner: run, Sonda: SondaVacia{}, Salida: &Registro{},
		})
		if err != nil || inf.Codigo != "esperando-persona" || inf.Publico {
			t.Fatalf("vuelta %d %+v %v", i, inf, err)
		}
	}
	w, _ := st.Load("123")
	if w.Fase == Escalado || w.FallosSeguidos != 0 {
		t.Fatalf("escaló esperando a una persona: %+v", w)
	}
}

type runnerPorNombre struct {
	porDefecto int
	ok         map[string]bool
	fn         func(dir string)
}

func (r runnerPorNombre) Run(_ context.Context, dir, nombre, _ string) (int, string, error) {
	if r.fn != nil {
		r.fn(dir)
	}
	if r.ok[nombre] {
		return 0, "", nil
	}
	return r.porDefecto, "", nil
}

func TestViajeExitCeroSinJourneyNoPublica(t *testing.T) {
	st, wt := preparar(t, cfgBueno)
	cat := filepath.Join(wt, "e2e", "catalogo.json")
	os.MkdirAll(filepath.Dir(cat), 0o755)
	os.WriteFile(cat, []byte(`{"viajes":[{"id":"admin","ac":["AC1"],"modo":"headless","cmd":"npx playwright test e2e/admin.spec.ts","artefacto":"e2e/evidencia/admin/journey.json"}]}`), 0o644)
	if err := PinCatalogo(st, "123", cat, []string{"AC1"}, ahoraFijo()); err != nil {
		t.Fatal(err)
	}
	run := &cuenta{exit: 0, fn: func(dir string) {
		os.MkdirAll(filepath.Join(dir, "e2e"), 0o755)
		os.WriteFile(filepath.Join(dir, "e2e", "report.json"), []byte("x"), 0o644)
	}}
	reg := &Registro{}
	inf, err := Publicar(context.Background(), st, "123", Opciones{
		Aplicar: true, Perfil: PerfilPorDefecto(), Runner: run, Sonda: SondaVacia{}, Salida: reg,
	})
	if err != nil || inf.Codigo != "viaje_sin_evidencia" || inf.Publico || len(reg.Llamadas) != 0 {
		t.Fatalf("%+v %v", inf, err)
	}
}
