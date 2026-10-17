# Local copy of github.com/jamesruan/sodium

Go binding for libsodium.

- Source: `github.com/jamesruan/sodium` v0.0.0-20181216154042-9620b83ffeae (whole module).
- License: MIT, see `LICENSE` (and `LICENSE.original` from the project it was forked from).
- Copied unchanged except: `go.mod` added (upstream has none; it holds the same `module github.com/jamesruan/sodium` line
  Go synthesizes for it), and `gofmt` in the .go files it flagged (whitespace and doc-comment layout in `sodium.go`).
- Needs libsodium (pkg-config `libsodium`) at build time, as before.
- Used through a `replace` in `chipper/go.mod`, so the import path stays the same. `README.md` is the upstream one; this file describes the local copy.
