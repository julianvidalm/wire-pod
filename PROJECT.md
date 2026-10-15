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

## Anthropic (Claude) provider

Select **Anthropic (Claude)** under *Knowledge Graph API Provider* in the web UI (`http://localhost:8080`) and enter:

- **Anthropic API Key**: a workspace-scoped key from console.anthropic.com. If left blank, `ANTHROPIC_API_KEY` from the chipper process environment is used.
- **Claude Model**: blank uses `claude-haiku-5-5` (fast, suited to spoken replies); `claude-opus-5-5` gives better answers at higher cost and latency.
- **LLM Prompt**: optional system prompt, cached on Anthropic's side.

It uses the official Go SDK (`github.com/anthropics/anthropic-sdk-go`) for knowledge-graph, intent-graph and camera (`getImage`) requests. Requests use effort `low`, disable thinking (except on models that cannot turn it off, such as `claude-opus-5-5`) and send no sampling parameters, which current Claude models reject. One client is reused per API key, failed requests are retried at most once, and the request starts while wire-pod is still connecting to the robot. The adapter lives in `chipper/pkg/wirepod/ttr/llm_anthropic.go`.

Measured time to first token on `claude-haiku-5-5` (median of 5): 731 ms with a new client and thinking, 406 ms with a reused client and thinking off.

Live check against the real API (never runs in CI):

```sh
ANTHROPIC_API_KEY=... go test -tags "nolibopusfile anthropic_live" -run Live ./pkg/wirepod/ttr/
```

chipper requires Go 1.26 and pins the `go1.26.9` toolchain (`go.mod`); `setup.sh` installs Go 1.26.9 on fresh machines, and an older local Go downloads the pinned toolchain on first build (`GOTOOLCHAIN=auto`). CI runs `govulncheck` on the vosk build.

## Releasing

```sh
git tag -a v0.1.0 -m "v0.1.0" && git push origin v0.1.0
```
