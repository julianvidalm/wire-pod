# Local copy of github.com/digital-dream-labs/hugh

Digital Dream Labs gRPC, logging and config helpers.

- Source: `github.com/digital-dream-labs/hugh` v0.0.0-20210210154335-f4159b9fcd5f (whole module).
- License: MIT, see `LICENSE`.
- Unchanged except `gofmt` in `http/client/functions.go`, `log/eventlog_formatter.go` and `log/syslog_formatter.go`
  (whitespace, doc-comment layout, and the `//go:build` lines gofmt adds next to the existing `// +build` lines).
- Used through a `replace` in `chipper/go.mod`, so the import path stays the same. `README.md` is the upstream one; this file describes the local copy.
