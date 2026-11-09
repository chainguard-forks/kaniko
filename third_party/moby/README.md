# Carried copy of Docker's `BuildArgs`

`builder/dockerfile/buildargs.go` is a byte-for-byte copy of
`github.com/docker/docker/builder/dockerfile/buildargs.go` at
`v28.5.2+incompatible`, the last release published on that module path. It
is the only piece of the Docker daemon kaniko needs: `pkg/dockerfile` embeds
`BuildArgs` so that `ARG` handling — the built-in proxy args kept out of the
image history, meta args before the first `FROM`, `--build-arg` precedence,
warnings about unused args — matches `docker build` exactly.

Why a copy rather than an import: `github.com/docker/docker` stops at
v28.5.2. Moby 29 is published as `github.com/moby/moby/api`,
`github.com/moby/moby/client` and the beta `github.com/moby/moby/v2` engine
module, where this file lives under `daemon/builder/dockerfile`. Importing the
original package compiled the whole builder package and some 45 daemon
packages into kaniko, and left the binary carrying Docker daemon advisories
that no version bump can ever fix.

The file is unmodified; its package name, `dockerfile`, is kept so the import
alias used by `pkg/dockerfile` stays the same. `utils.go` holds
`convertKVStringsToMap`, copied verbatim from `builder.go` of the same package
and version: it is the only helper `buildargs.go` calls outside its own file.
To compare against upstream:

    diff <(curl -sL https://raw.githubusercontent.com/moby/moby/v28.5.2/builder/dockerfile/buildargs.go) \
         third_party/moby/builder/dockerfile/buildargs.go

`LICENSE` and `NOTICE` are Docker's (Apache-2.0).
