# Concepts

Read when:

- you encounter a Maya Stall term you do not recognize;
- you are writing docs, issues, or PRs and want consistent vocabulary;
- you need the product nouns in one place.

This page is a glossary. For deeper domain wording, see the root
[`CONTEXT.md`](../CONTEXT.md).

## Product Boundary

**Maya Stall** - reusable test harness for real Autodesk Maya UI end-to-end
checks on owned Maya Hosts.

**maya-stall** - the command-line binary.

**Consuming Repo** - a repo that supplies non-secret Scenario config, Run
Payload paths, Maya Scripts, Plugin Artifacts, and Expected Outputs.

**Repo Run Config** - `.maya-stall.yaml` or `maya-stall.yaml`. Stores Scenario
definitions and must not store Host Credentials or private infrastructure.

**Crabbox** - upstream remote execution control plane used as a reference for
static SSH, target profiles, stop policy, visual evidence, artifacts, and review
publishing. Maya Stall is not a Crabbox fork.

## Targets And Hosts

**Target Profile** - a named target environment selected for a run. It maps to a
Host Pool in external host config.

**Host Pool** - a set of selectable Maya Hosts.

**Maya Host** - an owned Windows machine with Autodesk Maya, OpenSSH, an
interactive desktop, a writable work root, and a Session Broker.

**Host Credentials** - secrets and identity material needed to reach or use a
Maya Host. Keep them outside Repo Run Config.

**Maya Version Requirement** - the Autodesk Maya version a Scenario needs.

**Host Health** - layered readiness checks for config, SSH, work root, Session
Broker, Maya version, Visual Evidence, Host Lock, and Scenario inputs.

**Host Lock** - a claim that prevents more than one active Fresh Run from using
the same Maya Host at the same time.

## Run Lifecycle

**Scenario** - a named Maya end-to-end flow in Repo Run Config.

**Fresh Run** - a run that starts from a clean Maya UI Session and clean
workspace.

**Maya UI Session** - an interactive Autodesk Maya desktop process used for a
run. Raw SSH-launched service-session Maya is not accepted as UI proof.

**Debug Attach** - deliberate reuse of an existing Maya UI Session for
investigation.

**Run Attach** - read-only access to a run's events and Session Broker log via
`maya-stall attach`.

**Kept Session** - a session intentionally left open after a run for debugging.

**Stop Policy** - the cleanup rule that decides whether Maya Stall stops or
keeps a Maya UI Session after a run.

## Payloads And Results

**Run Payload** - repo-owned inputs staged for a Scenario: Plugin Artifacts,
Maya Scripts, scenes, Expected Outputs, and include paths.

**Plugin Artifact** - a built Maya plugin file or related loadable binary.

**Maya Script** - repo-owned script that drives Maya behavior inside the UI
Session.

**Expected Output** - repo-owned artifact or value that defines successful
Scenario behavior.

**Scenario Result** - structured JSON written by a Scenario with status,
summary, assertions, measurements, and produced outputs.

**Validator** - reusable Maya Stall check that compares run outputs against
Expected Outputs.

## Evidence And Publishing

**Evidence Bundle** - logs, screenshots, metadata, Scenario Result,
and output files returned from a run.

**Visual Evidence** - screenshots captured from the Maya UI Session.

**Evidence Store** - durable filesystem or network location where published
Evidence Bundles are kept.

**Review Comment** - published summary that links an Evidence Bundle into a code
review system such as GitHub or GitLab.

## Session Broker

**Session Broker** - Windows-side service that launches, owns, or attaches to
Maya UI Sessions for Maya Stall.

**Maya Session Daemon** - first Session Broker implementation used by Maya
Stall, currently represented by `gg_mayasessiond`.
