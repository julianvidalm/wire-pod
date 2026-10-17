#!/usr/bin/env bash
# Fills the local data directory with the files wire-pod uses but does not build, once, from their original
# publishers, checking pinned sha256 sums. After this nothing is downloaded at run time: the server installs
# Vosk models and setup.sh installs libvosk from this directory (see PROJECT.md, "Local dependencies").
#
# Usage: scripts/seed-local-data.sh [language ...]   (default: en-US es-ES)
# The directory is $V3CTOR_DATA, else $XDG_DATA_HOME/v3ctor, else ~/.local/share/v3ctor (one future Docker volume).
set -euo pipefail

DATA="${V3CTOR_DATA:-${XDG_DATA_HOME:-$HOME/.local/share}/v3ctor}"
LIB_DIR="$DATA/vosk/lib"
MODEL_DIR="$DATA/vosk/models"

VOSK_VER="0.3.45"
VOSK_ZIP="vosk-linux-x86_64-${VOSK_VER}.zip"
VOSK_SHA256="bbdc8ed85c43979f6443142889770ea95cbfbc56cffb5c5dcd73afa875c5fbb2"

# language -> "zip sha256"; only languages with a pinned sum can be seeded.
declare -A MODELS=(
  [en-US]="vosk-model-small-en-us-0.15.zip 30f26242c4eb449f948e42cb302dd7a686cb29a3423a8367f99ff41780942498"
  [es-ES]="vosk-model-small-es-0.42.zip 09b239888f633ef2f0b4e09736e3d9936acfd810bc65d53fad45261762c6511f"
)

# fetch <url> <dest> <sha256>: downloads unless dest already has that sum, then checks it.
fetch() {
  local url="$1" dest="$2" sum="$3"
  if [[ -f "$dest" ]] && echo "$sum  $dest" | sha256sum -c --status -; then
    echo "ok       $(basename "$dest")"
    return
  fi
  echo "fetching $(basename "$dest")"
  curl -fsSL -o "$dest.part" "$url"
  echo "$sum  $dest.part" | sha256sum -c --quiet -
  mv "$dest.part" "$dest"
}

mkdir -p "$LIB_DIR" "$MODEL_DIR"
fetch "https://github.com/alphacep/vosk-api/releases/download/v${VOSK_VER}/${VOSK_ZIP}" "$LIB_DIR/$VOSK_ZIP" "$VOSK_SHA256"

languages=("$@")
[[ ${#languages[@]} -eq 0 ]] && languages=(en-US es-ES)
for lang in "${languages[@]}"; do
  if [[ -z "${MODELS[$lang]:-}" ]]; then
    echo "no pinned model for $lang; known: ${!MODELS[*]}" >&2
    exit 1
  fi
  read -r zip sum <<<"${MODELS[$lang]}"
  fetch "https://alphacephei.com/vosk/models/$zip" "$MODEL_DIR/$zip" "$sum"
done

# SHA256SUMS lets the server check every model it installs.
(cd "$MODEL_DIR" && for lang in "${!MODELS[@]}"; do
  read -r zip sum <<<"${MODELS[$lang]}"
  [[ -f "$zip" ]] && echo "$sum  $zip"
done | sort -k2 >SHA256SUMS)
echo "data directory: $DATA"
