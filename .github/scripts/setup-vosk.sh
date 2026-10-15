#!/usr/bin/env bash
# Downloads libvosk and exports the cgo environment needed to build chipper with vosk.
# Usage: setup-vosk.sh  (writes to $GITHUB_ENV when running in GitHub Actions)
set -euo pipefail

VOSK_VER="0.3.45"
VOSK_DIR="vosk-linux-x86_64-${VOSK_VER}"
VOSK_SHA256="bbdc8ed85c43979f6443142889770ea95cbfbc56cffb5c5dcd73afa875c5fbb2"
DEST="${RUNNER_TEMP:-/tmp}/libvosk"

mkdir -p "$DEST"
curl -fsSL -o "$DEST/vosk.zip" \
  "https://github.com/alphacep/vosk-api/releases/download/v${VOSK_VER}/${VOSK_DIR}.zip"
echo "${VOSK_SHA256}  $DEST/vosk.zip" | sha256sum -c -
unzip -q -o "$DEST/vosk.zip" -d "$DEST"

LIB="$DEST/$VOSK_DIR"
{
  echo "CGO_ENABLED=1"
  echo "CGO_CFLAGS=-I$LIB"
  echo "CGO_LDFLAGS=-L$LIB -lvosk -ldl -lpthread"
  echo "LD_LIBRARY_PATH=$LIB"
  echo "VOSK_LIB=$LIB"
} >> "${GITHUB_ENV:-/dev/stdout}"
