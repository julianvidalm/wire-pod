# Local copy of github.com/kercre123/whisper.cpp/bindings/go

cgo binding for whisper.cpp, used by the experimental whisper.cpp speech-to-text engine.

- Source: `github.com/kercre123/whisper.cpp/bindings/go` v0.0.0-20250602164512-60cd96acff3a (whole module).
- License: MIT, see `LICENSE`.
- Unchanged except `gofmt` import order in `whisper_test.go` and `pkg/whisper/context_test.go`.
- Needs the whisper.cpp headers and libraries at build time, as before.
- Used through a `replace` in `chipper/go.mod`, so the import path stays the same. `README.md` is the upstream one; this file describes the local copy.
