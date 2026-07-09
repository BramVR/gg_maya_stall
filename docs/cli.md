# CLI

This is the command-surface overview for the `maya-stall` binary. It maps the
command tree, shared flags, config files, output rules, and exit behavior.

For per-command examples, see [commands/](commands/README.md). For vocabulary,
see [Concepts and glossary](concepts.md).

## Name

`maya-stall` - run Autodesk Maya UI Scenarios from repo-owned config and collect
review-ready evidence.

## Usage

```text
maya-stall [--help]
maya-stall version
maya-stall <command> [args]
```

Primary results go to stdout. Progress, diagnostics, and errors go to stderr.
Commands that mutate external systems, such as Review Comment publishing, also
support dry-run paths where available.

## Command Map

### Setup And Diagnostics

```text
maya-stall version
maya-stall init
maya-stall doctor [--host-config <path>] [--target-profile <name>] [--host <id>] [--scenario <name>] [--repair-trusted-plugin-allowlist]
```

See [version](commands/version.md), [init](commands/init.md), and
[doctor](commands/doctor.md).

### Run Lifecycle

```text
maya-stall run [host flags] [lock flags] [stop flags] <scenario>
maya-stall status [--run <run-id>]
maya-stall attach <run-id>
maya-stall attach <run-id> screenshot
maya-stall attach <run-id> control click --x <pixels> --y <pixels>
maya-stall stop <run-id>
```

Common host flags:

```text
--host-config <path>
--target-profile <name>
--host <id>
```

Lock and stop flags:

```text
--host-lock-wait <duration>
--host-lock-fail-fast
--keep-on-failure
--stop-after success|failure|always|never
```

See [run](commands/run.md), [status](commands/status.md),
[attach](commands/attach.md), and [stop](commands/stop.md).

### Visual Evidence

```text
maya-stall screenshot [host flags]
maya-stall record [host flags]
maya-stall control click --x <pixels> --y <pixels> [host flags] [--dry-run]
```

See [screenshot](commands/screenshot.md), [record](commands/record.md), and
[control](commands/control.md).
Scenarios can also enable screenshot and recording Visual Evidence in repo
config; `maya-stall run` and `maya-stall evidence collect` write those artifacts
into the Scenario Evidence Bundle.

When a Fresh Run or Kept Session already owns a Host Lock, use the run-scoped
`attach <run-id> screenshot` and `attach <run-id> control click` forms. They
verify the current Host Lock owner before touching the desktop.

### Evidence And Review Publishing

```text
maya-stall evidence collect [host flags] <scenario>
maya-stall evidence publish --destination <path> --base-url <url> <evidence-bundle-dir>
maya-stall review-comment github --repo <owner/name> --pr <number> [--token-env <name>] [--api-url <url>] [--dry-run] <published-evidence-dir>
maya-stall review-comment gitlab --project <path-or-id> --merge-request <iid> [--token-env <name>] [--base-url <url>] [--dry-run] <published-evidence-dir>
```

See [evidence](commands/evidence.md) and
[review-comment](commands/review-comment.md).

## Config Files

Repo config is YAML and must be non-secret:

```text
.maya-stall.yaml
maya-stall.yaml
```

Repo config contains Scenarios, Run Payload paths, Expected Outputs, Visual
Evidence policy, and Validators. It must not contain Host Credentials, private
hostnames, SSH keys, Windows users, license details, Host Pools, or Evidence
Store secrets.

Host config is passed explicitly:

```sh
maya-stall run --host-config ci-hosts.yaml --target-profile ci smoke
```

Host config may contain Target Profiles, Host Pools, fake diagnostic fields, or
opt-in real SSH transport. Keep it in user config, CI secrets, or runner-owned
files rather than in the consuming repo.
For real Maya plug-in runs, host config may also set
`trustedPluginArtifactsRoot` to a stable Maya Host directory that the operator
has added to Maya's trusted plug-in locations. `maya-stall doctor` validates
that Maya's durable `SafeModeAllowedlistPaths` preference contains that root,
Scenario-declared Plugin Artifact destinations, and parent directories for
nested `.mll` and Python Maya plug-ins under directory artifacts.
`maya-stall run` fails before staging Plugin Artifacts when the baseline is
missing. `maya-stall run` copies only declared `pluginArtifacts` there and
exposes the root as `MAYA_STALL_TRUSTED_PLUGIN_ARTIFACTS_ROOT`.

## Exit Behavior

`maya-stall` returns `0` on success and non-zero on usage errors, failed checks,
failed Scenarios, transport errors, validator failures, or publishing failures.

Scripts should branch on success versus non-zero and inspect stderr plus the
Evidence Bundle for details. Scenario failures are also recorded in
`evidence.json`.

## Output Artifacts

Default run artifacts are written under:

```text
artifacts/maya-stall/<run-id>/
```

Internal run state and Host Locks live under hidden repo-local state:

```text
.maya-stall/state/
```

Published bundles contain:

```text
artifact-manifest.json
review-comment.md
```
