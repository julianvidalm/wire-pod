# Fork notes

This is `julianvidalm/wire-pod`, a fork of [kercre123/wire-pod](https://github.com/kercre123/wire-pod) used by the v3ctor project to give Vector a Claude-powered brain.

## Remotes

```sh
git remote -v
# origin    git@github.com:julianvidalm/wire-pod.git
# upstream  https://github.com/kercre123/wire-pod.git (push disabled)
```

Sync with upstream through a pull request, never by pushing to `main`:

```sh
git fetch upstream
git checkout -b chore/sync-upstream origin/main
git merge upstream/main
git push -u origin chore/sync-upstream
gh pr create --fill
```

## Workflow

- `main` is protected: changes land through a pull request, the `build-test` check must pass, history stays linear (squash merge only), and force pushes and deletion are blocked.
- Branch names: `feat/...`, `fix/...`, `chore/...`. Commit messages follow Conventional Commits.

## CI/CD

| Workflow | Trigger | What it does |
|----------|---------|--------------|
| `ci.yml` | pull request, push to `main` | gofmt on changed Go files, `go vet`, `go test -race`, vosk build of chipper |
| `release.yml` | tag `v*` | builds `chipper` (vosk, linux/amd64) and publishes a GitHub Release with the tarball and its SHA-256 |
| `docker-publish.yml` | upstream only | skipped in this fork |
| Dependabot | weekly | GitHub Actions and Go module updates |

CI checks the packages listed by `.github/scripts/ci-packages.sh`. The coqui, leopard, whisper.cpp and experimental STT engines need extra native libraries and are excluded.

Run the same checks locally from `chipper/` (needs `libopus-dev`, `libogg-dev` and libvosk in `~/.vosk/libvosk`):

```sh
export CGO_ENABLED=1 CGO_CFLAGS="-I$HOME/.vosk/libvosk" \
  CGO_LDFLAGS="-L$HOME/.vosk/libvosk -lvosk -ldl -lpthread" LD_LIBRARY_PATH="$HOME/.vosk/libvosk"
go vet -tags nolibopusfile $(../.github/scripts/ci-packages.sh)
go test -race -tags nolibopusfile $(../.github/scripts/ci-packages.sh)
```

## Releasing

```sh
git tag -a v0.1.0 -m "v0.1.0" && git push origin v0.1.0
```
