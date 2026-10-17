# Local copy of github.com/fforchino/vector-go-sdk (partial)

Go SDK for Vector; chipper uses only `pkg/vector` and `pkg/vectorpb`.

- Source: `github.com/fforchino/vector-go-sdk` v0.0.0-20231108155304-62168f3595d6.
- License: MIT, see `LICENSE`.
- Copied: `pkg/vector`, `pkg/vectorpb` (the only packages chipper imports; they import nothing else from this module and
  have no `go:embed` or file paths), `go.mod`, `go.sum`, `LICENSE`, `README.md`.
- Left out: `data/` (about 9 MB of assets), `cmd/`, `proto/`, `pkg/oskrpb`, `pkg/sdk-wrapper`, `Makefile`, `setup.sh`,
  `start.sh` and the Python scripts. Copy them from the source version above if a new import needs them.
- Unchanged except `gofmt` whitespace (comment lines) in `pkg/vectorpb/cube.pb.go` and `pkg/vectorpb/messages.pb.go`.
- Used through a `replace` in `chipper/go.mod`, so the import path stays the same. `README.md` is the upstream one; this file describes the local copy.
