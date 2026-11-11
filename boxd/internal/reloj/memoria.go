package reloj

import "fmt"

// Perfil es el techo de una máquina. Los de edición son hipótesis hasta medir
// el RSS de un agente real. El burst es el de una suite (SUITE_GB).
type Perfil struct {
	EdicionHighMB int
	EdicionMaxMB  int
	BurstMB       int
	PresupuestoMB int
}

// PerfilPorDefecto sirve para una máquina de 16 GB. Se cambia con las opciones
// --presupuesto-mb, --burst-mb y --edicion-mb, o en el archivo de configuración.
func PerfilPorDefecto() Perfil { return PerfilDe(1200, 5120, 12*1024) }

// PerfilDe arma un perfil con el techo de edición, el de una corrida y el total de la máquina.
func PerfilDe(edicionMB, burstMB, presupuestoMB int) Perfil {
	return Perfil{EdicionHighMB: edicionMB * 2 / 3, EdicionMaxMB: edicionMB, BurstMB: burstMB, PresupuestoMB: presupuestoMB}
}

func (p Perfil) validar() error {
	if p.EdicionHighMB <= 0 || p.EdicionMaxMB <= p.EdicionHighMB {
		return &Error{Codigo: "perfil", Mensaje: "memory.high tiene que ser menor que memory.max"}
	}
	if p.BurstMB < p.EdicionMaxMB || p.PresupuestoMB < p.BurstMB {
		return &Error{Codigo: "perfil", Mensaje: "el burst no entra en el presupuesto del nodo"}
	}
	return nil
}

type CajaViva struct {
	ID    string
	MaxMB int
}

// CabeBurst suma los techos vivos y reemplaza el de esta caja por el burst.
// Una caja que no está en la lista se suma entera.
func CabeBurst(p Perfil, vivas []CajaViva, id string) bool {
	if err := p.validar(); err != nil {
		return false
	}
	suma := 0
	vio := false
	for _, v := range vivas {
		if v.ID == id {
			suma += p.BurstMB
			vio = true
			continue
		}
		suma += v.MaxMB
	}
	if !vio {
		suma += p.BurstMB
	}
	return suma <= p.PresupuestoMB
}

// ArgsDockerUpdate es el techo duro (memory.max). Docker no aplica memory.high:
// HighAplicadoPorDocker es falso a propósito, para no anotarlo como hecho.
func ArgsDockerUpdate(nombre string, maxMB int) []string {
	m := fmt.Sprintf("%dm", maxMB)
	return []string{"docker", "update", "-m", m, "--memory-swap", m, nombre}
}

func HighAplicadoPorDocker() bool { return false }
