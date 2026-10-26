# Patched copy of docker-credential-acr-env

This directory is derived from
[github.com/chrismellard/docker-credential-acr-env](https://github.com/chrismellard/docker-credential-acr-env)
at upstream commit `82a0ddb2758901b711d9d1614755b77e401598a1` (2023-03-04).
Upstream has been unmaintained since 2023 and has no fix for GO-2026-6225, so
kaniko carries and owns this copy.

It is an in-module copy: these are ordinary packages of the kaniko module
(`github.com/chainguard-dev/kaniko/third_party/docker-credential-acr-env/...`).
There is no separate `go.mod`, no `replace` directive, and the upstream module
path no longer appears in kaniko's `go.mod`, `vendor/` or build info. It is
used by:

- `pkg/creds`, which links `pkg/credhelper` into the executor and warmer
- `deploy/Dockerfile`, which builds the `docker-credential-acr-env` binary
  with `go install ./third_party/docker-credential-acr-env` and checks its
  build info and behaviour

Only `LICENSE`, `README.md`, `main.go`, `cmd/` and `pkg/` were copied. The
licence (Apache-2.0) and upstream copyright headers are kept.

## Local changes

1. GO-2026-6225: `pkg/credhelper/helper.go` replaces the unanchored host regex
   with an anchored, label-aware check (`isACRRegistry`): exact `host[:port]`
   only, valid port range, no trailing dot, no malformed labels, still
   case-sensitive. Non-ACR hosts now get a "credentials not found" error from
   `Get` rather than a generic error. Tests are in
   `pkg/credhelper/helper_test.go`; they run with kaniko's normal
   `go test ./...`.
2. Import paths rewritten from `github.com/chrismellard/docker-credential-acr-env/...`
   to `github.com/chainguard-dev/kaniko/third_party/docker-credential-acr-env/...`,
   and upstream's `go.mod` / `go.sum` removed. Dependencies (go-autorest,
   cobra, viper, docker-credential-helpers and so on) come from kaniko's own
   `go.mod`, so there is no separate dependency list to keep up to date.

Every local code change to the helper logic is marked with a
`Chainguard patch` comment. To review them, diff this directory against the
upstream commit above (ignoring the import path rewrite).

Ownership: because the upstream module path is gone, vulnerability databases
will not match any future advisory filed against
`github.com/chrismellard/docker-credential-acr-env` to kaniko. Whoever
maintains this fork should watch for such advisories and apply them here.

Why not `github.com/osscontainertools/docker-credential-acr`: it is the natural
long-term destination (it carries the full upstream history and fixed
GO-2026-6225), but as of 2026-10-26 it is not a drop-in for this repository.
v1.0.0 (2026-10-25) requires `go 1.27.0` while kaniko is on Go 1.26 in
`go.mod`, CI and the builder image. v0.8.0 is Go 1.26 compatible and closest to
upstream behaviour, but from v0.9.0 the fork adds an azidentity auth path behind
`FF_DOCKER_ACR_AZIDENTITY` (on by default in v1.0.0), which is a bigger
behavioural change than a credential-leak fix should carry. Its host check is
anchored but coarser than ours (no label validation, no `Host == input` guard).
Revisit when kaniko moves to Go 1.27, if we want advisory matching back.
