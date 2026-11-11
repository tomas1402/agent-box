package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIPublicaSoloSiElGateDejaElE2EFresco(t *testing.T) {
	raiz := t.TempDir()
	wt := filepath.Join(raiz, "wt")
	os.MkdirAll(wt, 0o755)
	conf := filepath.Join(raiz, "harness.json")
	os.WriteFile(conf, []byte(`{
	  "gates": [{"name": "test", "cmd": "mkdir -p e2e && touch e2e/report.json", "hard": true}],
	  "e2e": {"artifact": "e2e/report.json"},
	  "prTarget": "qa",
	  "branchPrefix": "feature/",
	  "prTitlePrefix": "[t] ",
	  "tracker": {"provider": "azure", "repo": "repo-demo"}
	}`), 0o644)
	estado := filepath.Join(raiz, "estado")
	base := []string{"--json", "--estado-dir", estado}

	var out bytes.Buffer
	if c := Ejecutar(append(base, "caja", "alta", "--wi", "321", "--worktree", wt, "--config", conf), &out, &bytes.Buffer{}); c != 0 {
		t.Fatalf("alta seca %d %s", c, out.String())
	}
	if _, err := os.Stat(filepath.Join(estado, "321.json")); !os.IsNotExist(err) {
		t.Fatal("el alta seca escribió estado")
	}

	out.Reset()
	if c := Ejecutar(append(append([]string{"--aplicar"}, base...), "caja", "alta", "--wi", "321", "--worktree", wt, "--config", conf), &out, &bytes.Buffer{}); c != 0 {
		t.Fatalf("alta %d %s", c, out.String())
	}

	// El agente reescribe el config. El pin tiene que ignorarlo.
	os.WriteFile(conf, []byte(`{"gates":[{"name":"test","cmd":"exit 1","hard":true}]}`), 0o644)

	out.Reset()
	var errb bytes.Buffer
	c := Ejecutar(append(append([]string{"--aplicar"}, base...), "publicar", "--wi", "321"), &out, &errb)
	if c != 0 {
		t.Fatalf("publicar %d\n%s\n%s", c, out.String(), errb.String())
	}
	var resp struct {
		OK    bool `json:"ok"`
		Datos struct {
			Informe struct {
				Publico bool   `json:"publico"`
				Fase    string `json:"fase"`
			} `json:"informe"`
			Argv [][]string `json:"argv"`
		} `json:"datos"`
	}
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatal(err, out.String())
	}
	// Con la salida por defecto (registro) es un ensayo: verde, con los argv
	// armados, pero el WI no queda listo ni se da por publicado.
	if !resp.OK || resp.Datos.Informe.Publico || resp.Datos.Informe.Fase == "listo" || !strings.Contains(out.String(), "ensayo_verde") {
		t.Fatalf("%s", out.String())
	}
	if strings.Contains(out.String(), `"subio":true`) {
		t.Fatalf("el CLI no llama a docker y dijo que subió la RAM: %s", out.String())
	}
	if len(resp.Datos.Argv) != 2 || !strings.Contains(strings.Join(resp.Datos.Argv[1], " "), "az repos pr create") {
		t.Fatalf("argv %v", resp.Datos.Argv)
	}

	out.Reset()
	c = Ejecutar(append(append([]string{"--aplicar"}, base...), "publicar", "--wi", "321"), &out, &bytes.Buffer{})
	if c != 0 || !strings.Contains(out.String(), "ensayo_verde") {
		t.Fatalf("un ensayo verde no tiene que bloquear el siguiente publicar: %d %s", c, out.String())
	}
}

func TestCLIPresupuestoNoCorreElGate(t *testing.T) {
	raiz := t.TempDir()
	wt := filepath.Join(raiz, "wt")
	os.MkdirAll(wt, 0o755)
	conf := filepath.Join(raiz, "harness.json")
	os.WriteFile(conf, []byte(`{
	  "gates": [{"name": "test", "cmd": "exit 7", "hard": true}],
	  "e2e": {"artifact": "e2e/report.json"},
	  "prTarget": "qa",
	  "branchPrefix": "feature/",
	  "tracker": {"provider": "azure", "repo": "repo-demo"}
	}`), 0o644)
	estado := filepath.Join(raiz, "estado")
	argsAlta := []string{"--json", "--aplicar", "--estado-dir", estado, "caja", "alta", "--wi", "7", "--worktree", wt, "--config", conf}
	if c := Ejecutar(argsAlta, &bytes.Buffer{}, &bytes.Buffer{}); c != 0 {
		t.Fatal(c)
	}
	vivas := "a:1200,b:1200,c:1200,d:1200,e:1200,f:1200,g:1200,h:1200"
	var out bytes.Buffer
	c := Ejecutar([]string{"--json", "--aplicar", "--estado-dir", estado, "--vivas", vivas, "publicar", "--wi", "7"}, &out, &bytes.Buffer{})
	if c == 0 || !strings.Contains(out.String(), "presupuesto") {
		t.Fatalf("codigo %d %s", c, out.String())
	}
	w, err := os.ReadFile(filepath.Join(estado, "7.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(w), `"fase": "admitido"`) {
		t.Fatalf("el presupuesto no tiene que mover la fase: %s", w)
	}
}
