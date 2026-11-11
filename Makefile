# agent-box: lo minimo para compilar, probar y ver una caja andando.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
PREFIX  ?= $(HOME)/.local

.PHONY: build test demo demo-etapas panel install clean ayuda

ayuda: ## esta lista
	@grep -E '^[a-z-]+:.*##' $(MAKEFILE_LIST) | sed -E 's/:.*## /\t/' | expand -t 14

build: ## compila boxd en bin/boxd
	cd boxd && go build -ldflags "-X main.version=$(VERSION)" -o ../bin/boxd ./cmd/boxd

test: ## tests de boxd (Go) y de boxctl y los scripts (Python, bash)
	cd boxd && go vet ./... && go test ./...
	bash tests/run.sh

demo: build ## una caja real en Docker: la crea, corre un gate adentro, sube y baja la RAM, la borra
	bash ops/demo-caja.sh

demo-etapas: build ## sin Docker: como boxd deduce la etapa (codigo, tests, e2e, gates) y frena un segundo publicar
	bash ops/demo-etapas.sh

panel: build ## el tablero de cajas en http://127.0.0.1:4319
	python3 boxctl/boxctl panel --servir

install: build ## copia boxd y boxctl a $(PREFIX)/bin
	install -d $(PREFIX)/bin
	install -m 755 bin/boxd boxctl/boxctl $(PREFIX)/bin/

clean: ## borra bin/
	rm -f bin/boxd
