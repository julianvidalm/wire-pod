# Project notes

`julianvidalm/wire-pod` is the voice and "cortex" server of the v3ctor project, which gives Vector a Claude-powered brain. It started from [kercre123/wire-pod](https://github.com/kercre123/wire-pod) (MIT, © 2022 Kerigan Creighton; see `LICENSE`) and is now developed independently: it is not a GitHub fork and does not track that repository.

## Independence

- `origin` (`git@github.com:julianvidalm/wire-pod.git`) is the only remote. Do not add the original repository as a remote, merge from it, or open pull requests against it.
- Pull requests come only from changes to this code base.
- If an upstream fix is ever worth having (for example a security issue), review it and port it by hand in a normal pull request that credits the source.
- Keep the MIT license text and copyright notice in `LICENSE`.

## Workflow

- `main` is protected: changes land through a pull request, the `build-test` check must pass, history stays linear (squash merge only), and force pushes and deletion are blocked.
- Branch names: `feat/...`, `fix/...`, `chore/...`. Commit messages follow Conventional Commits.

## CI/CD

| Workflow | Trigger | What it does |
|----------|---------|--------------|
| `ci.yml` | pull request, push to `main` | gofmt on changed Go files, `go vet`, `go test -race`, vosk build of chipper |
| `release.yml` | tag `v*` | builds `chipper` (vosk, linux/amd64) and publishes a GitHub Release with the tarball and its SHA-256 |
| Dependabot | weekly | GitHub Actions and Go module updates |

CI checks the packages listed by `.github/scripts/ci-packages.sh`. The coqui, leopard, whisper.cpp and experimental STT engines need extra native libraries and are excluded.

Run the same checks locally from `chipper/` (needs `libopus-dev`, `libogg-dev` and libvosk in `~/.vosk/libvosk`):

```sh
export CGO_ENABLED=1 CGO_CFLAGS="-I$HOME/.vosk/libvosk" \
  CGO_LDFLAGS="-L$HOME/.vosk/libvosk -lvosk -ldl -lpthread" LD_LIBRARY_PATH="$HOME/.vosk/libvosk"
go vet -tags nolibopusfile $(../.github/scripts/ci-packages.sh)
go test -race -tags nolibopusfile $(../.github/scripts/ci-packages.sh)
```

## MicTap receiver

Robots stream what they hear (and play) to wire-pod with MicTap: the gRPC service `v3ctor.mictap.v1.MicTap` (`chipper/pkg/mictap/mictappb/mictap.proto`) on the same TLS server and port as chipper. The robot is the client, so no port is open on the robot and the link works across NAT boundaries; vic-cloud forwards the stream using the chipper address from the robot's `server_config.json` and the pinned wire-pod certificate. Each `Stream` call carries the robot's ESN in the `x-vector-esn` metadata and the MicTap v1 byte stream (hello, then mic, speaker and stats frames) in chunks.

- `chipper/pkg/mictap`: v1 decoder, `Consume`, and the `Sink` port that consumers implement.
- `chipper/pkg/servers/mictap`: the gRPC adapter. `Session` is `Stream` plus a way back: while a robot keeps it open, `Sessions().Send(esn, command)` asks it to carry out an intent result or say a text, without wire-pod ever connecting to the robot.
- `/api-mictap/command` (web port 8080) sends one command by hand: `?serial=<esn>&intent=intent_clock_time[&params=<json>]` or `?serial=<esn>&say=<text>`.
- `micTapSink` in `chipper/pkg/initwirepod/mictap.go`: the active sink. It logs each stream (start, robot drop totals, end with sequence gaps) and, when a wake word model is configured, also runs the wake word detector.

The proto is canonical here; the firmware keeps an identical copy. Regenerate the Go code with `go generate ./pkg/mictap/mictappb/` (needs `protoc` and the plugin versions pinned in `generate.go`).

## Wake word detector

`chipper/pkg/wakeword` detects Vector's name in the robot's MicTap stream: "vector" or "víctor" anywhere in an utterance, in Spanish or English, independent of the robot's own "Hey Vector". It is off unless `WAKEWORD_VOSK_MODEL` points to a Vosk model directory; the Spanish small model (`vosk-model-small-es-0.42`) works best on Vector's microphone. For now a detection is only logged (`Wake word: <esn> heard "vector" ...`); starting a conversation from it is still to be designed.

- Whole words only: "director" and "lector" never match. Matches below confidence 0.6 are ignored ("sin nombre tuyo" was heard as "sin vector" at 0.54).
- Partial recognition results are checked every 100 ms, so the name is caught while the user keeps talking; each spoken name wakes once.
- Echo handling level 1: while Vector speaks or moves (MicTap flags), and for 300 ms after, the recognizer gets silence. Detections closer than 1.5 s are merged.
- Ports: `Recognizer` (Vosk adapter in `pkg/wakeword/voskrec`) and `Handler` (what to do on a detection).

Evaluate it on recordings of Vector's microphone (16 kHz mono WAV) without a robot:

```sh
go build -tags nolibopusfile -o wakeword-eval ./cmd/wakeword-eval
./wakeword-eval -model ~/vosk-model-small-es-0.42 clip1.wav clip2.wav
```

## Anthropic (Claude) provider

In the web UI (`http://localhost:8080`) open **Server → Assistant**, choose the provider **Anthropic (Claude)** and enter:

- **API key**: a workspace-scoped key from console.anthropic.com. If left blank, `ANTHROPIC_API_KEY` from the chipper process environment is used.
- **Model**: blank uses `claude-haiku-5-5` (fast, suited to spoken replies); `claude-opus-5-5` gives better answers at higher cost and latency.
- **Personality prompt**: optional system prompt, cached on Anthropic's side.

It uses the official Go SDK (`github.com/anthropics/anthropic-sdk-go`) for knowledge-graph, intent-graph and camera (`getImage`) requests. Requests use effort `low`, disable thinking (except on models that cannot turn it off, such as `claude-opus-5-5`) and send no sampling parameters, which current Claude models reject. One client is reused per API key, failed requests are retried at most once, and the request starts while wire-pod is still connecting to the robot. The adapter lives in `chipper/pkg/wirepod/ttr/llm_anthropic.go`.

Measured time to first token on `claude-haiku-5-5` (median of 5): 731 ms with a new client and thinking, 406 ms with a reused client and thinking off.

Live check against the real API (never runs in CI):

```sh
ANTHROPIC_API_KEY=... go test -tags "nolibopusfile anthropic_live" -run Live ./pkg/wirepod/ttr/
```

chipper requires Go 1.26 and pins the `go1.26.9` toolchain (`go.mod`); `setup.sh` installs Go 1.26.9 on fresh machines, and an older local Go downloads the pinned toolchain on first build (`GOTOOLCHAIN=auto`). CI runs `govulncheck` on the vosk build.

## Web UI

The pages in `chipper/webroot` share one design system: `css/style.css` (color tokens, light and dark themes, accent
colors), `assets/icons.svg` (our icons; use them as `<svg class="icon"><use href="/assets/icons.svg#name"></use></svg>`)
and `js/shell.js` (top bar, theme, toasts that replace `alert()`, and section tabs that open from the URL hash, for
example `index.html#log`). No page loads scripts from other hosts. The upstream update check is gone: **Home → About**
shows the commit the server was built from (`/api/get_version_info` returns `{"commit", "version"}` without network
calls).

Browser tests serve the pages with the API answered by fixtures, so they need neither the server nor a robot:

```sh
python3 -m venv .venv && .venv/bin/pip install playwright pytest && .venv/bin/playwright install chromium
.venv/bin/pytest chipper/webtest
```

## Local dependencies

The robot is personal, so wire-pod depends on no one else's servers at run time. Files it uses but does not build
live in one local data directory, `$V3CTOR_DATA` or `~/.local/share/v3ctor` (a Docker volume if it is ever packaged):

| Path | What | Used by |
|------|------|---------|
| `vosk/lib/vosk-linux-x86_64-0.3.45.zip` | libvosk | `setup.sh` |
| `vosk/models/vosk-model-small-*.zip` + `SHA256SUMS` | Vosk models | Server settings → Language (installed into `vosk/models/<lang>/model`, checked against `SHA256SUMS`) |
| `robot/ota/*.ota` | Robot firmware files | `/api/get_ota/<file>` (robots updating or recovering through this server) |
| `robot/vic-cloud` | vic-cloud binary | SSH robot setup in packaged builds (source builds use `../vector-cloud/build/vic-cloud`) |

`scripts/seed-local-data.sh [language ...]` fills it once from the original publishers (alphacep releases and
alphacephei.com) with pinned sha256 sums; English and Spanish are pinned. A language whose zip is missing fails with
a message that says where to copy it. The Vosk Go binding is kept in `chipper/third_party/vosk-api-go` (Apache-2.0)
and used through a `replace`. CI and the Dockerfile still download libvosk from alphacep with the pinned sum.

## Releasing

```sh
git tag -a v0.1.0 -m "v0.1.0" && git push origin v0.1.0
```
