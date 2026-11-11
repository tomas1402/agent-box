package reloj

import (
	"context"
	"time"
)

// vivo envuelve al runner: antes de cada gate deja en el estado qué se está
// corriendo, le pone plazo y mide cuánto tardó. Así el panel ve el avance de
// un publicar mientras corre, no recién al final.
type vivo struct {
	st      Store
	w       *WI
	dentro  Runner
	ahora   func() time.Time
	tiempos map[string]int64
}

func (v *vivo) Run(ctx context.Context, dir, nombre, cmd string) (int, string, error) {
	v.w.ponerEtapa(etapaDeGate(nombre), nombre, v.ahora())
	_ = v.st.Save(*v.w)
	plazo := GateTimeoutDefault
	for _, g := range v.w.Gates {
		if g.Nombre == nombre && g.TimeoutSec > 0 {
			plazo = time.Duration(g.TimeoutSec) * time.Second
		}
	}
	ctx, cancel := context.WithTimeout(ctx, plazo)
	defer cancel()
	t0 := time.Now()
	exit, cola, err := v.dentro.Run(ctx, dir, nombre, cmd)
	v.tiempos[nombre] = time.Since(t0).Milliseconds()
	return exit, cola, err
}

// cerrarCorrida anota el resumen de la corrida y deja la etapa como quedó el
// worktree. Devuelve el WI listo para guardar.
func cerrarCorrida(ctx context.Context, w *WI, inf *Informe, tipo string, inicio time.Time, t0 time.Time, tiempos map[string]int64, g Git, ahora time.Time) {
	c := &Corrida{Tipo: tipo, Inicio: inicio.Format(time.RFC3339), MS: time.Since(t0).Milliseconds(), Codigo: inf.Codigo}
	for i := range inf.Gates {
		gi := &inf.Gates[i]
		gi.MS = tiempos[gi.Nombre]
		c.Gates = append(c.Gates, GateResumido{
			Nombre: gi.Nombre, Exit: gi.Exit, MS: gi.MS,
			Verde: gi.Exit == 0 || gi.Preexisting || gi.Reemplazado,
		})
	}
	w.Corrida = c
	w.evento(ahora, "corrida", tipo, inf.Codigo)
	if w.Fase == Listo {
		w.Actividad = nil
		return
	}
	if g != nil {
		if a, err := Observar(ctx, g, *w, ahora); err == nil {
			w.ponerEtapa(a.Etapa, a.Detalle, ahora)
			w.Actividad.Archivos = a.Archivos
			w.Actividad.UltimoCambio = a.UltimoCambio
			return
		}
	}
	w.ponerEtapa(EtapaInactiva, "terminó "+tipo, ahora)
}
