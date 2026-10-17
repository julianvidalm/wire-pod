# Local copy of github.com/digital-dream-labs/api

Protocol definitions and generated Go code for Vector's cloud APIs (chipper, jdocs, token).

- Source: `github.com/digital-dream-labs/api` v0.0.0-20210824232136-8cc90c1bb12c.
- License: MIT, see `LICENSE`.
- Left out: `cpp/`, `csharp/`, `javascript/` and `python/` (generated clients for other languages, about 2.3 MB).
  Everything else is copied, including `go/`, `proto/`, `go.mod` and `go.sum`.
- Unchanged except `gofmt` in `tools.go` (adds the `//go:build tools` line next to the existing `// +build tools`).
- Used through a `replace` in `chipper/go.mod`, so the import path stays the same. `README.md` is the upstream one; this file describes the local copy.
