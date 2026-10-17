# Vosk Go binding (local copy)

cgo binding for the Vosk speech recognition library (libvosk), kept in this repository so builds do not fetch it
from someone else's repository.

- Source: `github.com/kercre123/vosk-api/go` v1.0.2, a fork of [alphacep/vosk-api](https://github.com/alphacep/vosk-api)
  (`go/` directory), copied unchanged except this README and `go.mod` (its self-`replace` dropped).
- License: Apache-2.0, see `COPYING`.
- Used through `replace github.com/kercre123/vosk-api/go => ./third_party/vosk-api-go` in `chipper/go.mod`, so the
  import path stays the same.
- Needs libvosk at build and run time (headers and `libvosk.so`), as before.
