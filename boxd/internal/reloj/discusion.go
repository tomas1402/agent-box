package reloj

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Solo estas líneas de la salida de un gate entran al comentario. Las escriben
// las herramientas de la caja (lint-diff, jest-nuevos). El resto de la cola no
// se copia nunca: ahí puede aparecer un secreto.
var prefijosDeNota = []string{"jest: ", "lint: ", "ya estaba roto en ", "inestable "}

const topeNotas = 6

// Prueba es un test de e2e que pasó, tal como lo nombra el reporte.
type archivoE2E struct {
	Ruta    string
	Pasaron int
	Titulos []string
}

// leerE2E saca del reporte de jest qué archivos de e2e corrieron y cuántos tests
// pasaron en cada uno. De otros formatos no saca títulos.
func leerE2E(reporte, worktree string) []archivoE2E {
	b, err := os.ReadFile(reporte)
	if err != nil {
		return nil
	}
	var j struct {
		Resultados []struct {
			Nombre string `json:"name"`
			Casos  []struct {
				Titulo string `json:"title"`
				Estado string `json:"status"`
			} `json:"assertionResults"`
		} `json:"testResults"`
	}
	if json.Unmarshal(b, &j) != nil {
		return nil
	}
	var out []archivoE2E
	for _, r := range j.Resultados {
		rel, err := filepath.Rel(worktree, r.Nombre)
		if err != nil || strings.HasPrefix(rel, "..") {
			rel = filepath.Base(r.Nombre)
		}
		a := archivoE2E{Ruta: filepath.ToSlash(rel)}
		for _, c := range r.Casos {
			if c.Estado == "passed" {
				a.Pasaron++
				a.Titulos = append(a.Titulos, c.Titulo)
			}
		}
		out = append(out, a)
	}
	sort.Slice(out, func(i, k int) bool { return out[i].Ruta < out[k].Ruta })
	return out
}

// seccion devuelve las líneas de una sección "## titulo" de la descripción del PR.
func seccion(desc, titulo string) []string {
	var out []string
	dentro := false
	for _, l := range strings.Split(desc, "\n") {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "## ") {
			dentro = strings.EqualFold(strings.TrimSpace(strings.TrimPrefix(t, "## ")), titulo)
			continue
		}
		if dentro && t != "" {
			out = append(out, t)
		}
	}
	return out
}

func lista(b *strings.Builder, items []string) {
	b.WriteString("<ul>")
	for _, it := range items {
		b.WriteString("<li>" + html.EscapeString(strings.TrimPrefix(it, "- ")) + "</li>")
	}
	b.WriteString("</ul>")
}

func recortar(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// armarDiscusion es el comentario que el reloj deja en el work item: qué cambió,
// qué e2e lo prueban, qué gates corrió él y qué quedó avisado. Lo lee una persona.
func armarDiscusion(ctx context.Context, w WI, inf Informe, opt Opciones) string {
	var b strings.Builder
	rama := w.Rama
	if rama == "" {
		rama = w.BranchPrefix + w.ID
	}
	fmt.Fprintf(&b, "<b>Reloj %s: gates en verde, PR abierto de <code>%s</code> a <code>%s</code></b>",
		html.EscapeString(w.ID), html.EscapeString(rama), html.EscapeString(w.PRTarget))

	if cambia := seccion(opt.Descripcion, "Qué cambia"); len(cambia) > 0 {
		b.WriteString("<p><b>Qué cambia</b></p>")
		lista(&b, cambia)
	}

	// E2E: con título, los de archivos que la rama agregó o tocó. El resto, por cantidad.
	if archivos := leerE2E(inf.E2ERuta, w.Worktree); len(archivos) > 0 {
		tocados := map[string]bool{}
		if opt.Git != nil && w.PRTarget != "" {
			if ts, err := opt.Git.Tocados(ctx, w.Worktree, "origin/"+w.PRTarget); err == nil {
				for _, t := range ts {
					tocados[t] = true
				}
			}
		}
		total := 0
		var propios, resto []string
		for _, a := range archivos {
			total += a.Pasaron
			if tocados[a.Ruta] {
				for i, t := range a.Titulos {
					if i == 8 {
						propios = append(propios, fmt.Sprintf("y %d más en %s", len(a.Titulos)-8, a.Ruta))
						break
					}
					propios = append(propios, recortar(t, 160))
				}
			} else {
				resto = append(resto, fmt.Sprintf("%s (%d)", a.Ruta, a.Pasaron))
			}
		}
		fmt.Fprintf(&b, "<p><b>E2E: %d pasaron, ninguno salteado</b></p>", total)
		if len(propios) > 0 {
			b.WriteString("<p>De esta rama:</p>")
			lista(&b, propios)
		}
		if len(resto) > 0 {
			b.WriteString("<p>Ya existentes, corridos de nuevo: " + html.EscapeString(strings.Join(resto, ", ")) + "</p>")
		}
	}

	b.WriteString("<p><b>Gates corridos por el reloj, fuera de la sesión del agente</b></p><ul>")
	for _, g := range inf.Gates {
		fmt.Fprintf(&b, "<li>%s: ", html.EscapeString(g.Nombre))
		switch {
		case g.Preexisting:
			fmt.Fprintf(&b, "exit %d, igual que en la base", g.Exit)
		case g.Reemplazado:
			b.WriteString("reemplazado por un viaje nuevo")
		case g.Exit == 0:
			b.WriteString("ok")
		default:
			fmt.Fprintf(&b, "exit %d", g.Exit)
		}
		if g.MS >= 1000 {
			fmt.Fprintf(&b, " (%d s)", g.MS/1000)
		}
		b.WriteString("</li>")
	}
	b.WriteString("</ul>")

	var notas []string
	for _, g := range inf.Gates {
		for _, l := range strings.Split(g.Cola, "\n") {
			l = strings.TrimSpace(l)
			for _, p := range prefijosDeNota {
				if strings.HasPrefix(l, p) && len(notas) < topeNotas {
					notas = append(notas, recortar(l, 220))
				}
			}
		}
	}
	if len(notas) > 0 {
		b.WriteString("<p><b>Avisos de los gates</b></p>")
		lista(&b, notas)
	}
	if revisar := seccion(opt.Descripcion, "A tener en cuenta al revisar"); len(revisar) > 0 {
		b.WriteString("<p><b>A tener en cuenta al revisar</b></p>")
		lista(&b, revisar)
	}
	b.WriteString("<p>Nada fue mergeado ni deployado.</p>")
	return b.String()
}

// Comentar arma el comentario de un informe ya corrido y lo manda por la salida.
func Comentar(ctx context.Context, st Store, id string, inf Informe, opt Opciones) (string, error) {
	w, err := st.Load(id)
	if err != nil {
		return "", err
	}
	txt := armarDiscusion(ctx, w, inf, opt)
	if opt.Salida == nil {
		return txt, nil
	}
	prov, ok := ProveedorDe(w.Tracker)
	if !ok {
		return txt, &Error{Codigo: "tracker_desconocido", Mensaje: w.Tracker.Provider}
	}
	return txt, opt.Salida.Comentar(ctx, prov.Comentario(w, txt))
}
