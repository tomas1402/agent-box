# Contribuir

1. `make test` tiene que pasar. No necesita Docker ni red.
2. Si tocás `boxd`, corré también `make demo` (necesita Docker).
3. Un cambio de comportamiento entra con su test. En `boxd` van junto al código (`*_test.go`); en
   `boxctl`, en `tests/`.
4. `boxctl` tiene un contrato JSON versionado (`docs/contrato.md` y `docs/contrato.schema.json`).
   Agregar campos o verbos no lo rompe; cambiar el significado de uno existente, sí.
5. Mensajes de commit en español, en presente, diciendo qué cambia para quien usa la herramienta.

El código y los mensajes están en español. Los issues y PRs pueden ir en español o en inglés.
