<!--
 Copyright 2026 Dimitri Koshkin. All rights reserved.
 SPDX-License-Identifier: Apache-2.0
 -->

# kmirror

[![build](https://github.com/dkoshkin/kmirror/actions/workflows/build.yaml/badge.svg)](https://github.com/dkoshkin/kmirror/actions/workflows/build.yaml)
[![codecov](https://codecov.io/github/dkoshkin/kmirror/graph/badge.svg)](https://codecov.io/github/dkoshkin/kmirror)

kmirror swaps a running in-cluster Kubernetes controller for a binary you built locally, without modifying the
Deployment. It wraps [mirrord][mirrord] so your binary runs with the Pod's network, DNS, environment and
ServiceAccount, takes over the controller's leader-election Lease so only your local copy reconciles,
and hands control back to the in-cluster controller when you exit.

> **Status:** early development.

## Usage

```bash
kmirror -n NAMESPACE -d DEPLOYMENT BINARY [-- EXTRA_ARGS...]
```

For example, to run a local build of the [Kubemark provider][capk] in place of the in-cluster one:

```bash
CGO_ENABLED=1 go build -o bin/manager .
kmirror -n capk-system -d capk-controller-manager ./bin/manager -- --v=4
```

Press Ctrl-C to stop the local manager and hand control back to the in-cluster one.

## How it works

1. **Discovers the setup from the Deployment.** It picks the `manager` container (or the first container),
   its probe ports, the ports to steal (typically the `9443` webhook port), the secret and projected mounts
   to read from the Pod (webhook serving certs, ServiceAccount token), and the leader-election Lease in the
   Deployment's namespace that is held by one of its Pods.
2. **Builds the command line.** It inherits the container's flags, drops the ones that don't make sense locally
   (`--kubeconfig`, leader election, metrics, health and profiler bind addresses), expands `$(VAR)` references,
   adds `--leader-elect=false` and appends anything after `--`.
3. **Checks the binary.** On Linux, mirrord injects with `LD_PRELOAD`, so the binary must be dynamically linked
   (`CGO_ENABLED=1 go build ...`). kmirror fails early on a static binary.
4. **Takes over leadership.** It patches the Lease to its own holder identity and keeps renewing it.
   The in-cluster manager loses leadership, restarts once, and comes back as a Ready standby that does not reconcile.
5. **Runs your binary under mirrord.** Only the webhook port is stolen, so kubelet probes still reach the real
   container. `KUBECONFIG` is unset, so your binary authenticates as the Pod's ServiceAccount.
6. **Cleans up on exit.** It deletes the Lease if it still holds it, and the in-cluster manager resumes
   reconciling within seconds.

## Prerequisites

- [kubectl][kubectl] access to the target cluster
- [mirrord][mirrord] CLI on your `PATH`
- A locally built controller binary (dynamically linked on Linux)

## Development

### Setup

1. Install [Devbox][Devbox] (creates the toolchain via Nix).
2. Install [direnv][direnv] and run `direnv allow`.
3. Install the [pre-commit][pre-commit-hooks] git hooks with `pre-commit install`.

Run `make` to list all targets with descriptions.

### Test

```bash
make test
```

`make test` downloads Kubernetes envtest assets (`setup-envtest`, pinned by `ENVTEST_VERSION` in the Makefile)
and runs unit and integration tests with race detection and coverage.

### Build

```bash
make build-snapshot
```

The binary for your OS is placed in `./dist`, e.g. `./dist/kmirror_darwin_arm64_v8.0/kmirror`.

### Lint

```bash
make lint
make pre-commit
```

### e2e tests

```bash
make e2e-test
```

e2e tests live in `test/e2e` (Ginkgo, `e2e` build tag). The target:

1. Creates a KinD cluster and installs Cluster API, CAPD and the Kubemark provider (CAPK) with `clusterctl`.
2. Shallow-clones CAPK at the installed version and builds its manager with `CGO_ENABLED=1`.
3. Runs that manager locally under kmirror in place of `capk-system/capk-controller-manager`.
4. Creates a workload cluster with a CAPD control plane and a Kubemark worker, and waits for the worker Machine
   to join as a Node while kmirror holds the Lease.
5. Stops kmirror and checks the in-cluster manager takes the Lease back.

Versions are pinned in [`make/clusterctl.mk`](make/clusterctl.mk) and [`make/e2e.mk`](make/e2e.mk).

The CAPK controller and Kubemark images are published for `linux/amd64` only, so the e2e tests need an amd64
Docker host. They run in CI on every PR.

### Local cluster

```bash
make kind.create
eval "$(make kind.kubeconfig)"
```

### Docs

```bash
make preview-docs
```

The docs site in [`docs/`](docs/) is built with Hugo and published to GitHub Pages on every push to `main`.

## Releases

Releases are automated with [release-please][release-please-action].
Merging the release PR tags the release and runs [goreleaser][goreleaser], which publishes binaries for
Linux and macOS (`amd64`, `arm64`) to [GitHub Releases](https://github.com/dkoshkin/kmirror/releases).

PR titles must follow [Conventional Commits](https://www.conventionalcommits.org/).

## Repository automation

- [Dependabot][Dependabot] updates GitHub Actions, Go modules and docs npm dependencies weekly.
  Dependabot PRs are auto-merged once checks pass.
- [Devbox update][devbox-action] updates Devbox packages and the Go version weekly.
- Dependabot consolidation uses Claude Code to fix and combine Dependabot PRs that fail checks.

### Required secrets

| Secret                        | Used by                                    |
|-------------------------------|--------------------------------------------|
| `DEPENDENCY_AUTOMATION_TOKEN` | Devbox update, Dependabot consolidation    |
| `GIT_SSH_SIGNING_PRIVATE_KEY` | Signing automated commits                  |
| `GIT_USER_NAME`               | Author of automated commits                |
| `GIT_USER_EMAIL`              | Author of automated commits                |
| `CLAUDE_CODE_OAUTH_TOKEN`     | Dependabot consolidation                   |
| `CODECOV_TOKEN`               | Coverage upload in unit tests              |

`DEPENDENCY_AUTOMATION_TOKEN` is a fine-grained [GitHub PAT][Github-PAT] for this repository with
read and write access to Contents, Pull requests and Issues.

[Devbox]: https://www.jetify.com/docs/devbox/installing-devbox
[direnv]: https://direnv.net/
[pre-commit-hooks]: https://pre-commit.com/#3-install-the-git-hook-scripts
[mirrord]: https://github.com/metalbear-co/mirrord
[kubectl]: https://kubernetes.io/docs/tasks/tools/
[capk]: https://github.com/kubernetes-sigs/cluster-api-provider-kubemark
[release-please-action]: https://github.com/googleapis/release-please-action
[goreleaser]: https://goreleaser.com/
[Github-PAT]: https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/managing-your-personal-access-tokens
[Dependabot]: https://docs.github.com/en/code-security/getting-started/dependabot-quickstart-guide
[devbox-action]: https://github.com/dkoshkin/kmirror/actions/workflows/devbox-dependencies-update.yaml
