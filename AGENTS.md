# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

Dagger modules and Go libraries behind Basis' GitLab CI/CD. A consumer project declares its targets in `ci/pipeline.toml`, includes the `basis/iac/ci-templates` template, and the `orchestrator` module does the rest. `README.adoc` is the spec: the schema, every orchestrator function, and the reasoning behind each design choice. Read the relevant section before changing behaviour, and update it in the same commit.

## Libraries, modules and their dependencies

There is no root `go.mod` and no `go.work`. Each directory is its own Go module:

| Kind | Directory | Module path | Depends on | Release tag |
|---|---|---|---|---|
| Library | `gitlabci/` | `github.com/BasisTI/daggerverse/gitlabci` | none | `gitlabci/vX.Y.Z` |
| Library | `pipeline/` | `github.com/BasisTI/daggerverse/pipeline` | `gitlabci` (by tag) | `pipeline/vX.Y.Z` |
| Dagger module | `orchestrator-utils/` | `dagger/orchestrator-utils` | none | `X.Y.Z`, shared |
| Dagger module | `uv/` | `dagger/uv` | `pipeline` (by tag) | `X.Y.Z`, shared |
| Dagger module | `maven/`, `npm/` | `dagger/maven`, `dagger/npm` | `pipeline`, `gitlabci` (by tag) | `X.Y.Z`, shared |
| Dagger module | `orchestrator/` | `dagger/orchestrator` | `pipeline`, `gitlabci` (by tag), `maven`, `npm`, `uv`, `orchestrator-utils` (local source) | `X.Y.Z`, shared |

Two kinds of edge:

- **By tag.** These are the `go.mod` requires. A library edit stays invisible to its consumers until the library is tagged and the consumer's `go.mod` is bumped. Each consumer pins its own version, so they differ: `maven`, `npm` and `uv` still pin `pipeline v0.9.1` while `orchestrator` pins `v0.12.0`. Bump only the consumers whose code uses the change.
- **Local source.** These are the `dependencies` in `orchestrator/dagger.json`. A change in `maven`, `npm`, `uv` or `orchestrator-utils` reaches the orchestrator directly, through the generated client in `orchestrator/internal/dagger/`. Run `dagger develop` in `orchestrator/` to regenerate that client after changing a module's function signature.

The modules share one repository-wide tag. Consumers pin it through `DAGGER_MODULE` in ci-templates as `github.com/BasisTI/daggerverse/orchestrator@X.Y.Z`. The `go.mod` files reference the library tags, such as `pipeline v0.12.0`.

## Releasing

A change ships as a chain of **release units**, in dependency order: `gitlabci`, then `pipeline`, then the Dagger modules. Each library that changed is its own unit. All module changes together form the last unit, because the modules share one tag. Each unit goes through four steps:

1. Commit.
2. Open a PR.
3. Squash-merge the PR.
4. Tag the merge commit on `main` and push the tag.

A unit starts only after the previous unit's tag is pushed, because its `go get` resolves that tag. Tag `3.17.0` is why. It was cut from a commit where `orchestrator` used `pipeline` code that was never tagged. That orchestrator does not compile, so every project pinned to it fails.

### Plan

Any plan for a change that touches a library ends with a **Deployment** section. It lists every step of every unit, in order, with concrete branch names and versions. For a feature in `pipeline` and `maven`:

1. `pipeline`: commit the `pipeline/` change on branch `TG-xxx-pipeline`.
2. `pipeline`: open the PR.
3. `pipeline`: squash-merge it.
4. `pipeline`: tag the merge commit `pipeline/v0.14.0` and push the tag.
5. Modules: on branch `TG-xxx`, commit the `maven/` change plus `go get github.com/BasisTI/daggerverse/pipeline@v0.14.0` in `maven/`.
6. Modules: open the PR.
7. Modules: squash-merge it.
8. Modules: tag the merge commit `3.18.0` and push the tag.

Pick each version from the latest tag on the remote (`git ls-remote --tags origin`):

- Patch for a fix.
- Minor for a feature.
- Libraries stay on `v0.x`.

A library change already merged to `main` but never tagged shrinks its unit to the tag step.

End the plan by asking the user to confirm it and to authorize all of its steps. Pushes, merges and tags are public, so wait for that answer.

### Execute

Run the authorized steps one at a time, and check each result before starting the next. If a step fails, stop and report. A later unit built on a missing tag is how 3.17.0 broke.

- **Branch and commit:** see Conventions below. Library units append `-<lib>` to the branch name, so they do not collide with the module unit of the same card.
- **PR:** `gh pr create --base main`, with the commit subject as the title.
- **Merge:** `gh pr merge <n> --squash`, then `git switch main && git pull`. Tag the squash commit that lands on `main`.
- **Library tag:** lightweight. Run `git tag pipeline/vX.Y.Z <sha> && git push origin pipeline/vX.Y.Z`.
- **Module tag:** annotated. The message is the PR title without the `- TG-xxx` suffix. Run `git tag -a X.Y.Z -m "<title>" <sha> && git push origin X.Y.Z`.
- **Before committing the module unit:** run `go get` in every module that uses the new library code, then pass the gate below.

The gate compiles the orchestrator, and with it the four modules it reads by source:

```sh
dagger call -m ./orchestrator --source ./orchestrator/testdata --config-path triagem.toml validate
```

It must print the resolved targets and the derived images. A Go compile error means a library tag is missing or a `go.mod` was not bumped. A plain `go build ./...` inside a module checks the same thing, provided `dagger develop` has generated the code first.

### Summary

Finish with a table that has one row per unit: the unit, the new tag, the tagged commit (short SHA and subject) and the PR link.

## Commands

Libraries use plain Go:

```sh
cd pipeline && go test ./...
cd pipeline && go test ./config -run TestName
```

Dagger modules need a few extra steps:

- **Generated code.** `dagger.gen.go` and `internal/` are gitignored. Run `dagger develop` inside the module directory to (re)generate them. That command also bumps `engineVersion` in `dagger.json` to your local engine; revert that line unless the bump is intended.
- **Tests need an engine session.** The generated `init` panics with `DAGGER_SESSION_PORT is not set` under a bare `go test`. Run tests as `dagger run go test ./...`, or `dagger run go test -run TestName ./...` for a single test.
- **Opt-in engine tests.** The engine/Sonar tests in `maven/` are skipped unless `TG278_ENGINE_TESTS=1` is set; the Sonar ones also read `TG278_SONAR_*`. See the header of `maven/reuse_engine_test.go`.
- **Check the module compiles with `dagger call`, as in the release gate.** `dagger functions` has listed the functions of a module that `dagger call` then failed to compile. To run a function against a project:

  ```sh
  dagger call -m ./orchestrator --source <project-dir> --config-path ci/pipeline.toml validate
  ```

## Architecture

The flow is: `ci/pipeline.toml` → `pipeline/config` (strict parse, defaults, validation) → `orchestrator` (turns each resolved target into a build strategy and a quality strategy, in `targets.go` and `strategies.go`) → `pipeline.PublishAll` / `pipeline.CheckQuality` (generic over the Dagger SDK types via `DaggerOps`) → the per-technology modules (`maven`, `npm`, `uv`) and `orchestrator-utils` (git, version bump and commit, `crane` registry operations, security report index).

Invariants that span several files:

- **Anti-drift.** The image map used by `check-images` and `promote` is derived from the targets (`Config.ProjectImages()`), never declared. Do not add a second list of images.
- **Custom targets.** `type = "custom"` targets are built by a project-local module (`DAGGER_MODULE: "."`) that loads the same `pipeline.toml` through `pipeline/config`. The generic `publish-all` and `check-quality` refuse them. `check-images` and `promote` still cover them.
- **Adding a schema field** touches several places:
  - `pipeline/config/config.go` and `validate.go`. Parsing rejects unknown fields.
  - The resolved-target mapping in `orchestrator/targets.go`.
  - Often `pipeline/types.go` and `orchestrator-utils`.
  - The schema tables in `README.adoc`.
  - The real-project fixtures in `pipeline/config/testdata/` and `orchestrator/testdata/`.
- **The version bump commit** is pushed with `git push -o ci.skip`. Its message is `Bump versão para <v>`, deliberately without `[skip ci]` (see README "ci.skip").
- **GitLab reporting is optional and fail-soft.** Commit statuses and MR notes run only when every `--gitlab-*` parameter is present. Failures to post notes are logged and swallowed.
- **Source-scanning guard tests.** For example, `orchestrator-utils/promote_test.go` requires `WithRegistryAuth` on every `From` of a non-literal image.

## Conventions

- Branches are named after the Taiga card, like `TG-300`.
- Commit messages are in Portuguese and start with an infinitive verb. They end with the Taiga card when there is one, like `Adicionar suporte a arquivo de versão plain - TG-303`. The body explains why.
- Code comments, log output and error messages are in Portuguese. `README.adoc` is in English.
