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

## Analysing without rebuilding (`analysis-inputs`, `analyze-from-reports`)

`full-build` runs `clean verify`, the Sonar analysis and the Jib publish in one go. The `develop`
branch pipeline already runs the tests in the publish job, and running them again for the analysis
doubled its time, so the analysis can be split from the build:

- `analysis-inputs` takes the `tree` of `full-build` (the module directory after the build) and keeps
  the `target/` of the module and of every child module, by exclusion: jars, archives, HTML and
  assets, logs, filtered resources and key material stay out, and only the `*.class` files of
  `classes/` and `test-classes/` stay in. Reports written to custom directories by the pom are kept at
  the paths they have.
- `analyze-from-reports` mounts the source, puts that directory back on the module tree and runs only
  `sonar:sonar`. The scanner goal resolves the dependency classpath by itself, so nothing is compiled
  and no test runs. In reactor mode a `-DskipTests install` stage puts the sibling modules in `~/.m2`
  first, because the analysis runs as `-f <module>/pom.xml`.

`analyze-from-reports` does not check what it is given. The caller must only pass inputs of the same
commit and complete; the orchestrator does that with a manifest (see the root README).

A normal `full-build` already ran the scanner as a second Maven invocation over the `target/` of the
first one; the split changes where that `target/` comes from, not what the scanner sees.

Tests against a real build (aggregator with a child module, reports in custom paths) need the Dagger
engine and the network: `TG278_ENGINE_TESTS=1 dagger run go test -run TestEngine ./...`.
