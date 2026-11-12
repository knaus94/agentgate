# Releasing

[Русский](ru/releasing.md)

## Publish a version

```sh
# 1. update CHANGELOG.md
git commit -am "chore(release): v0.2.0"
git tag v0.2.0
git push origin main v0.2.0
```

Pushing a `v*` tag runs [.github/workflows/release.yml](../.github/workflows/release.yml):

1. `go vet` and `go test` on the tagged commit;
2. static builds for `linux/amd64` and `linux/arm64`, with the version stamped in;
3. `agentgate_linux_<arch>.tar.gz` for each, plus `checksums.txt`;
4. a GitHub Release with generated notes.

Nothing is published from a developer machine. A tag that fails its tests publishes nothing.

Version numbers follow semantic versioning: a change to the plan schema, the state format or a command's
input is a minor version before 1.0 and a major one after.

## How users get it

First install:

```sh
curl -fsSL https://raw.githubusercontent.com/knaus94/agentgate/main/scripts/install.sh | sh
```

Update:

```sh
agentgate update --check   # is there a newer release?
agentgate update           # download, verify, replace, refresh the /dev command
```

`update` downloads the archive for the running platform, compares its SHA-256 with `checksums.txt`, replaces
the binary atomically and runs the new binary's `install` so the `/dev` command matches it. A running task is
not disturbed: its state format is read by the new binary.

Pin a version: `AGENTGATE_VERSION=v0.2.0 sh install.sh`.
Install elsewhere: `AGENTGATE_BIN=/usr/local/bin sh install.sh`.
Release mirror: `AGENTGATE_BASE_URL=https://mirror.example/releases sh install.sh`.

## Forks

The release repository is compiled in. Build with your own:

```sh
go build -ldflags "-X github.com/knaus94/agentgate/internal/update.Repo=you/agentgate \
                   -X github.com/knaus94/agentgate/internal/update.Version=0.2.0" ./cmd/agentgate
```

and set `REPO` in `scripts/install.sh`.
