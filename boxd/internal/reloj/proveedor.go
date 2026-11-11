package reloj

import (
	"encoding/json"
	"strings"
)

// Proveedor es la integración con donde viven el ticket y el PR. boxd arma los
// comandos y los ejecuta con la CLI de cada uno (az, gh), que es la que tiene
// la sesión. Sumar otro es implementar esta interfaz y registrarlo abajo.
type Proveedor interface {
	// Comentario devuelve el comando que deja el comentario en el work item.
	Comentario(w WI, html string) []string
	// PR devuelve el comando que abre el pull request.
	PR(w WI, rama, titulo, descripcion string) []string
	// URL saca del stdout del comando de PR la dirección del pull request.
	URL(stdout string) string
	// Falta dice qué dato del harness le falta a este proveedor, o vacío.
	Falta(w WI) string
}

var proveedores = map[string]Proveedor{
	"azure":  azure{},
	"github": github{},
}

// ProveedorDe devuelve la integración del harness. "ninguno" (o vacío) no tiene:
// los gates deciden y nadie escribe afuera.
func ProveedorDe(t Tracker) (Proveedor, bool) {
	p, ok := proveedores[t.Provider]
	return p, ok
}

func sinProveedor(t Tracker) bool { return t.Provider == "" || t.Provider == "ninguno" }

// ---------- Azure DevOps (CLI az) ----------

type azure struct{}

func (azure) Falta(w WI) string {
	if w.Tracker.Repo == "" {
		return "tracker.repo"
	}
	return ""
}

func (azure) Comentario(w WI, html string) []string {
	return append([]string{"az", "boards", "work-item", "update", "--id", w.ID, "--discussion", html},
		append(argsDestino(w.Tracker, false), "--output", "none")...)
}

func (azure) PR(w WI, rama, titulo, descripcion string) []string {
	return append([]string{
		"az", "repos", "pr", "create",
		"--repository", w.Tracker.Repo,
		"--source-branch", rama,
		"--target-branch", w.PRTarget,
		"--title", titulo,
		"--work-items", w.ID,
		"--description", descripcion,
	}, append(argsDestino(w.Tracker, true), "--output", "json")...)
}

func (azure) URL(stdout string) string {
	var pr struct {
		ID         int `json:"pullRequestId"`
		Repository struct {
			WebURL string `json:"webUrl"`
		} `json:"repository"`
	}
	if json.Unmarshal([]byte(stdout), &pr) != nil || pr.ID == 0 {
		return ""
	}
	if pr.Repository.WebURL == "" {
		return "PR " + itoa(pr.ID)
	}
	return pr.Repository.WebURL + "/pullrequest/" + itoa(pr.ID)
}

// ---------- GitHub (CLI gh) ----------
// tracker.repo es "dueño/repo". El work item es un issue de ese repo.

type github struct{}

func (github) Falta(w WI) string {
	if !strings.Contains(w.Tracker.Repo, "/") {
		return "tracker.repo como dueño/repo"
	}
	return ""
}

func (github) Comentario(w WI, html string) []string {
	// GitHub muestra bien el HTML simple del comentario (b, p, ul, li, code).
	return []string{"gh", "issue", "comment", w.ID, "--repo", w.Tracker.Repo, "--body", html}
}

func (github) PR(w WI, rama, titulo, descripcion string) []string {
	return []string{"gh", "pr", "create", "--repo", w.Tracker.Repo, "--head", rama, "--base", w.PRTarget,
		"--title", titulo, "--body", strings.TrimSpace(descripcion) + "\n\nRefs #" + w.ID}
}

func (github) URL(stdout string) string {
	lineas := strings.Split(strings.TrimSpace(stdout), "\n")
	u := strings.TrimSpace(lineas[len(lineas)-1])
	if strings.HasPrefix(u, "http") {
		return u
	}
	return ""
}
