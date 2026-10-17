# Local copy of github.com/currantlabs/ble

Bluetooth Low Energy library, kept in this repository so builds do not fetch it from someone else's repository.

- Source: `github.com/currantlabs/ble` v0.0.0-20171229162446-c1d21c164cf8 (whole module).
- License: BSD-3-Clause style, see `LICENSE`.
- Copied unchanged except: `go.mod` added (upstream has none; it holds the same `module github.com/currantlabs/ble` line
  Go synthesizes for it), and `gofmt` in `darwin/device.go`, `linux/gatt/server.go`, `linux/hci/socket/dummy.go` and
  `linux/hci/socket/socket.go` (whitespace, plus the `//go:build` lines gofmt adds next to the existing `// +build` lines).
- Used through a `replace` in `chipper/go.mod`, so the import path stays the same. `README.md` is the upstream one; this file describes the local copy.
