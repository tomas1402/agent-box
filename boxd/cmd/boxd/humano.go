package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// humano imprime el resultado para una persona. Con --json sale el objeto entero;
// sin --json, esto: lo mismo, en pocas líneas y sin tener que saber la forma del JSON.
func humano(w io.Writer, op string, ok bool, datos any, codigo, mensaje string) {
	var d map[string]any
	if b, err := json.Marshal(datos); err == nil {
		_ = json.Unmarshal(b, &d)
	}
	if inf, si := d["informe"].(map[string]any); si {
		d = inf
	}
	switch {
	case d["wi"] != nil && d["memoria"] != nil: // un informe: publicar, paso, preparar
		informe(w, op, d)
	case d["cajas"] != nil:
		tabla(w, d["cajas"].([]any))
	case d["fase"] != nil && d["id"] != nil:
		if v, si := d["veredicto"].(map[string]any); si && op == "veredicto" {
			que := "rechazado"
			if v["aprobado"] == true {
				que = "aprobado"
			}
			sha, _ := v["sha"].(string)
			if len(sha) > 8 {
				sha = sha[:8]
			}
			fmt.Fprintf(w, "veredicto anotado: commit %s %s por %v. Un commit nuevo pide revisión nueva.\n", sha, que, v["quien"])
		}
		unaCaja(w, d)
	case d["spec"] != nil:
		spec, _ := d["spec"].(map[string]any)
		if d["codigo"] == "seco" {
			fmt.Fprintf(w, "%s (sin --aplicar): crearía la caja %v con techo de %v MB\n", op, spec["Nombre"], spec["MemoryMaxMB"])
		} else if d["creada"] == true {
			fmt.Fprintf(w, "work item %v admitido; caja %v creada (techo %v MB, runtime %s)\n", d["wi"], spec["Nombre"], spec["MemoryMaxMB"], o(spec["Runtime"], "el de docker"))
		} else {
			fmt.Fprintf(w, "work item %v admitido, sin caja (falta --docker)\n", d["wi"])
		}
	case d["version"] != nil:
		fmt.Fprintf(w, "boxd %v\n", d["version"])
	case ok:
		fmt.Fprintf(w, "%s: ok\n", op)
	}
	if !ok && codigo != "" && d["memoria"] == nil {
		fmt.Fprintf(w, "%s: %s", op, codigo)
		if mensaje != "" {
			fmt.Fprintf(w, " (%s)", mensaje)
		}
		fmt.Fprintln(w)
	}
}

func o(v any, si string) string {
	if s, _ := v.(string); s != "" {
		return s
	}
	return si
}

func informe(w io.Writer, op string, d map[string]any) {
	gates, _ := d["gates"].([]any)
	for _, x := range gates {
		g, _ := x.(map[string]any)
		exit, _ := g["exit"].(float64)
		ms, _ := g["ms"].(float64)
		marca := "ok  "
		switch {
		case exit == -1 && ms == 0:
			marca = "..  " // en seco: todavía no corrió
		case g["preexisting"] == true:
			marca = "base" // falla igual que en la base
		case exit != 0:
			marca = "ROJO"
		}
		fmt.Fprintf(w, "  %s  %-22v exit %-4.0f %5.0f s\n", marca, g["nombre"], exit, ms/1000)
	}
	// De un gate rojo, el final de su salida: es lo que hay que leer para arreglarlo.
	for _, x := range gates {
		g, _ := x.(map[string]any)
		exit, _ := g["exit"].(float64)
		cola, _ := g["cola"].(string)
		if exit <= 0 || g["preexisting"] == true || strings.TrimSpace(cola) == "" {
			continue
		}
		lineas := strings.Split(strings.TrimRight(cola, "\n"), "\n")
		if len(lineas) > 15 {
			lineas = lineas[len(lineas)-15:]
		}
		fmt.Fprintf(w, "\n  --- %v, últimas líneas ---\n", g["nombre"])
		for _, l := range lineas {
			fmt.Fprintf(w, "  | %s\n", l)
		}
	}
	codigo, _ := d["codigo"].(string)
	switch {
	case d["publico"] == true:
		if pr, _ := d["pr"].(string); pr != "" {
			fmt.Fprintf(w, "\npublicado: %s\n", pr)
		} else {
			fmt.Fprintln(w, "\npublicado: los gates dieron cero y el work item quedó listo. El harness no tiene tracker, así que no se abrió PR ni se comentó nada.")
		}
	case codigo == "ensayo_verde":
		fmt.Fprintln(w, "\nensayo_verde: los gates dieron cero. No se escribió nada; con --salida ejecutar se publica de verdad.")
	case codigo == "seco":
		fmt.Fprintln(w, "\nsin --aplicar: estos son los gates que correría.")
	case codigo != "":
		fmt.Fprintf(w, "\n%s: %s (fase: %v)\n", op, codigo, d["fase"])
		if det, _ := d["detalle"].(string); det != "" {
			fmt.Fprintf(w, "  %s\n", det)
		}
	case op == "paso" || op == "preparar":
		fmt.Fprintf(w, "\n%s: ok\n", op)
	}
}

func unaCaja(w io.Writer, c map[string]any) {
	fmt.Fprintf(w, "work item %v · %v", c["id"], c["fase"])
	if a, si := c["actividad"].(map[string]any); si {
		fmt.Fprintf(w, " · %v %v", a["etapa"], o(a["detalle"], ""))
	}
	fmt.Fprintln(w)
	if t := o(c["titulo"], ""); t != "" {
		fmt.Fprintln(w, " ", t)
	}
	fmt.Fprintf(w, "  worktree: %v\n", c["worktree"])
	if g := o(c["ultimoGate"], ""); g != "" {
		fmt.Fprintf(w, "  frenado en %s, %v de 3 fallos seguidos\n", g, c["fallosSeguidos"])
	}
	if co, si := c["corrida"].(map[string]any); si {
		fmt.Fprintf(w, "  última corrida (%v):", co["tipo"])
		gs, _ := co["gates"].([]any)
		for _, x := range gs {
			g, _ := x.(map[string]any)
			marca := "ok"
			if g["verde"] != true {
				marca = "ROJO"
			}
			fmt.Fprintf(w, " %v=%s", g["nombre"], marca)
		}
		fmt.Fprintln(w)
	}
}

func tabla(w io.Writer, cajas []any) {
	if len(cajas) == 0 {
		fmt.Fprintln(w, "no hay cajas. Se crean con: boxd --docker --aplicar caja alta --wi ID --worktree DIR --config harness.json")
		return
	}
	fmt.Fprintf(w, "%-10s %-11s %-12s %-12s %s\n", "WI", "FASE", "ETAPA", "RAM", "AHORA")
	for _, x := range cajas {
		c, _ := x.(map[string]any)
		etapa, detalle, ram := "-", "", "sin caja"
		if a, si := c["actividad"].(map[string]any); si {
			etapa, detalle = o(a["etapa"], "-"), o(a["detalle"], "")
		}
		if m, si := c["metricas"].(map[string]any); si {
			ram = fmt.Sprintf("%.0f/%.0f MB", m["memMb"], m["techoMb"])
		} else if _, hay := c["metricas"]; !hay {
			ram = "-"
		}
		fmt.Fprintf(w, "%-10v %-11v %-12s %-12s %s\n", c["id"], c["fase"], etapa, ram, detalle)
	}
}
