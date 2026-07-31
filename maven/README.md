# Dagger Module for Maven Builds

## Testcontainers (`--use-docker`)

The Maven image ships no Docker daemon, so a suite that uses Testcontainers fails inside this
module with `Could not find a valid Docker environment`. Worse, it fails quietly: the default CI
options include `-Dmaven.test.failure.ignore=true`, so the build still reports `BUILD SUCCESS`
while those tests never ran.

`--use-docker` binds a Docker-in-Docker daemon to the build and points `DOCKER_HOST` at it. Test
code is unchanged — a `@Testcontainers` suite just works.

```go
dag.Maven(dagger.MavenOpts{
    BuildImage: "maven:3.9.11-eclipse-temurin-25-alpine",
    UseDocker:  true,
})
```

```sh
dagger call --use-docker=true full-build --source=. --module=my-app \
    --commit-sha=$(git rev-parse HEAD) --version=1.0.0 stdout
```

It is off by default because it costs a `dind` container per build, and most modules do not need
one. Two details are load-bearing and easy to get wrong if this is ever reimplemented:

- The daemon's `/var/lib/docker` is mounted on a cache volume. Left on the container filesystem it
  sits on Dagger's overlayfs, and every image pull dies extracting the first whiteout entry with
  `failed to convert whiteout file ...: operation not permitted`.
- `TESTCONTAINERS_RYUK_DISABLED=true`. The reaper has nothing to reap — the daemon is discarded
  with the service — and reaching it back through the service binding is unreliable.

The storage volume uses `PRIVATE` sharing, so concurrent builds do not corrupt each other's
daemon. The tradeoff is that images are pulled fresh on each run.
