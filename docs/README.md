# Maya Stall Docs

**Run real Autodesk Maya UI Scenarios from repo-owned config.**

## What Maya Stall Is

Maya Stall is a Go CLI for real Autodesk Maya desktop UI end-to-end testing.
It keeps test intent in the consuming repo while moving the fragile desktop work
onto an owned Windows Maya Host. A repo declares named Scenarios, Run Payload
paths, Expected Outputs, and Validators. Maya Stall stages those inputs, runs the
Scenario through a Session Broker, captures Visual Evidence, writes an Evidence
Bundle, and can publish a Review Comment with durable artifact links.

It is for plugin maintainers, CI maintainers, Scenario authors, and reviewers
who need more than headless pass/fail. Use it when a real Maya UI Session,
screenshots, recordings, logs, and structured Scenario Results are part of the
proof. It is not a generic CI runner, a secrets manager, a hostile multi-tenant
sandbox, or a replacement for repo-owned build steps.

Maya Stall uses Crabbox as a design and implementation reference for remote
execution, stop policy, visual evidence, artifacts, and review publishing, but
it keeps a Maya-specific product boundary: owned Maya Hosts, a Session Broker,
typed Run Payloads, Scenario Results, Maya version checks, and review-ready
Evidence Bundles.

## How It Fits Together

```text
consuming repo              maya-stall CLI             Windows Maya Host
-------------               --------------             -----------------
.maya-stall.yaml  ----->    select Scenario      SSH   clean run workspace
payload paths                stage payload       ---->  Session Broker
validators                   collect evidence    <----  Maya UI Session
review target                publish comment            screenshots/video
```

The CLI is a Go binary (`cmd/maya-stall`, `internal/cli`). The consuming repo
owns non-secret Scenario config and all domain-specific Maya scripts. Host
Pools, Host Credentials, private hostnames, SSH keys, license details, and
Evidence Store paths stay in user or CI configuration. The Windows Maya Host
provides OpenSSH, a writable work root, an interactive desktop, Autodesk Maya,
and a Session Broker such as `gg_mayasessiond`.

Default tests and default commands use fake/local transport. Real SSH is opt-in
through host config outside the consuming repo.

## A Run, End To End

1. The CLI loads repo config from `.maya-stall.yaml` or `maya-stall.yaml`.
2. It selects a named Scenario and resolves Target Profile, Host Pool, and Maya
   Host from external host config when provided.
3. It acquires a Host Lock so one Fresh Run uses a Maya Host at a time.
4. It stages only declared Run Payload paths into clean run state.
5. It asks the Session Broker to launch or use a Maya UI Session.
6. It provides a Scenario Result path through `MAYA_STALL_SCENARIO_RESULT`.
7. It collects Visual Evidence, logs, metadata, outputs, and result JSON into an
   Evidence Bundle.
8. It runs generic Validators and records failures in `evidence.json`.
9. It applies the Stop Policy: clean up, keep on failure, or keep/stop according
   to explicit flags.
10. Optional publish commands copy the Evidence Bundle to an Evidence Store and
    create or update one marked Review Comment.

## Install And Check

This repo currently builds from source:

```sh
go test ./...
go build -o bin/maya-stall ./cmd/maya-stall
```

Then verify the command surface:

```sh
./bin/maya-stall version
./bin/maya-stall --help
```

## Quick Start

```sh
maya-stall init
maya-stall doctor --scenario smoke
maya-stall run smoke
maya-stall evidence collect smoke
```

Publish a completed Evidence Bundle:

```sh
maya-stall evidence publish \
  --destination /mnt/evidence/maya-stall \
  --base-url https://evidence.example.com/maya-stall \
  artifacts/maya-stall/<run-id>
```

Create or update a review comment from the published bundle:

```sh
maya-stall review-comment github \
  --repo owner/repo \
  --pr 123 \
  /mnt/evidence/maya-stall/<run-id>
```

## Where To Read Next

Pick whichever matches your intent:

- **Start here:** [Getting started](getting-started.md),
  [CLI overview](cli.md), [Concepts and glossary](concepts.md).
- **Use the CLI:** [Command reference](commands/README.md),
  [init](commands/init.md), [doctor](commands/doctor.md),
  [run](commands/run.md), [evidence](commands/evidence.md),
  [review-comment](commands/review-comment.md).
- **Prepare real hosts:** [Windows Maya Host setup](setup/windows-maya-host.md).
- **Understand the product boundary:** [Maya Stall V1 PRD](prd/0001-maya-stall-v1.md),
  [ADRs](adr/), [Source map](source-map.md).
- **Help agents work here:** [Domain docs](agents/domain.md),
  [Issue tracker](agents/issue-tracker.md),
  [Windows Maya Host agent notes](agents/windows-maya-host.md).

## About These Docs

Markdown in this directory is the user-facing documentation source.
Implementation truth stays in code. Use the [Source map](source-map.md) before
changing behavior claims, and keep ADRs focused on decisions rather than command
reference text.
