package reloj

import (
	"os"
	"syscall"
)

// Trabar toma el lock del WI (flock sobre <id>.lock). Con esperar en falso
// vuelve enseguida y avisa si otro proceso lo tiene. Dos publicar sobre el
// mismo WI, o un latido en medio de un publicar, se pisarían el estado.
func (s Store) Trabar(id string, esperar bool) (soltar func(), ocupado bool, err error) {
	p, err := s.path(id)
	if err != nil {
		return nil, false, err
	}
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return nil, false, err
	}
	f, err := os.OpenFile(p[:len(p)-len(".json")]+".lock", os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, false, err
	}
	modo := syscall.LOCK_EX
	if !esperar {
		modo |= syscall.LOCK_NB
	}
	if err := syscall.Flock(int(f.Fd()), modo); err != nil {
		f.Close()
		if err == syscall.EWOULDBLOCK {
			return nil, true, nil
		}
		return nil, false, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, false, nil
}
