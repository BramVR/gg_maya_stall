# Maya Stall

**Run real Autodesk Maya UI Scenarios from repo-owned config.**

Maya Stall is a Go CLI for end-to-end checks that must happen inside an
interactive Autodesk Maya desktop. It lets a consuming repo declare named
Scenarios, stage only the required payload paths, run those Scenarios on an
owned Maya Host, collect visual and structured evidence, and publish a review
comment with links to the result.

```sh
maya-stall run smoke
```

Behind that command, Maya Stall loads repo config, selects a Target Profile and
Maya Host, stages the Run Payload, asks the Session Broker to run the Scenario
inside Maya, captures an Evidence Bundle, runs Validators, and cleans up or
keeps the session according to the Stop Policy.

## Who Maya Stall Is For

Maya Stall fits teams that maintain Maya plugins, tools, or scenes where a
headless check is not enough:

- plugin maintainers who need proof that a build loads and behaves in real Maya;
- CI maintainers who own Windows Maya Hosts and want deterministic UI evidence;
- Scenario authors who want repo-owned scripts, outputs, and Validators without
  storing host credentials in the repo;
- reviewers who need screenshots, recordings, logs, metadata, and structured
  Scenario Results linked from normal code review.

Use Maya Stall for owned-host Maya UI proof. Do not use it as a generic remote
execution runner, a CI replacement, a secrets store, or a security boundary
between untrusted users.

## How It Works

```text
consuming repo              maya-stall CLI             Windows Maya Host
-------------               --------------             -----------------
.maya-stall.yaml  ----->    select Scenario      SSH   clean run workspace
payload paths                stage payload       ---->  Session Broker
validators                   collect evidence    <----  Maya UI Session
review target                publish comment            screenshots/video
```

- **CLI** - Go binary under `cmd/maya-stall`. Owns config loading, Scenario
  selection, Host Pool selection, payload staging, evidence layout, Validators,
  publishing, and Review Comments.
- **Consuming repo** - owns non-secret Repo Run Config, Maya Scripts, scenes,
  Plugin Artifacts, Expected Outputs, and domain-specific assertions.
- **Maya Host** - an owned Windows machine with Autodesk Maya, OpenSSH, an
  interactive desktop, a writable work root, and a Session Broker such as
  `gg_mayasessiond`.
- **Evidence Store** - a filesystem or network location where completed Evidence
  Bundles are copied and made linkable from review comments.

Crabbox is a reference for remote execution, stop policy, desktop evidence, and
artifact discipline, but Maya Stall is Maya-specific and does not require the
Crabbox binary at runtime.

## Quick Start

```sh
go test ./...
mkdir -p bin
go build -o bin/maya-stall ./cmd/maya-stall
./bin/maya-stall --help
```

Create repo-only config:

```sh
maya-stall init
```

Run the generated fake Scenario:

```sh
maya-stall doctor --scenario smoke
maya-stall run smoke
maya-stall evidence collect smoke
```

Capture standalone Visual Evidence:

```sh
maya-stall screenshot
maya-stall record
```

The fake broker supports screenshots and recordings. The `gg_mayasessiond`
broker captures screenshots through `viewport.capture`; recording reports an
actionable unsupported error until the daemon exposes a recording tool.

Prepare real hosts with the
[Windows Maya Host setup checklist](docs/setup/windows-maya-host.md), then keep
Host Pools, hostnames, SSH keys, Windows users, license details, Session Broker
paths, and Evidence Store paths outside `.maya-stall.yaml`.

## Docs

Start with [Maya Stall Docs](docs/README.md), then read:

- [Getting started](docs/getting-started.md)
- [CLI overview](docs/cli.md)
- [Command reference](docs/commands/README.md)
- [Concepts and glossary](docs/concepts.md)
- [Windows Maya Host setup](docs/setup/windows-maya-host.md)
- [Source map](docs/source-map.md)

Architectural decisions live in [docs/adr](docs/adr/). The v1 product shape is
captured in [docs/prd/0001-maya-stall-v1.md](docs/prd/0001-maya-stall-v1.md).
