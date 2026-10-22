#!/usr/bin/env bash
# Lists the chipper packages CI can build without optional native STT engines.
# coqui, leopard, whisper.cpp (stt/whisper.cpp and the cortex's whispercpp) and experimental commands need extra
# native libraries.
# Run from the chipper directory.
set -euo pipefail

go list -tags nolibopusfile ./... | grep -Ev '/(coqui|leopard|experimental|whisper\.cpp|whispercpp)(/|$)'
