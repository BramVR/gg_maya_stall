# PR And Merge Proof

Read when preparing, reviewing, or landing a PR.

## Proof Manifest

Every PR closeout should include a Proof Manifest from `scripts/proof/select-proof.mjs`.
The manifest records changed files, whether live Maya proof is required, why it
was required, and the status of local, docs, artifact, and live Maya gates.

The checked-in policy is `proof/live-maya-policy.json`. If a PR changes live
product behavior, the selector writes `live_maya_required=true`.

Live-required surfaces include:

- Target Profile, Host Pool, host config, SSH, SFTP, or Windows host execution;
- `gg_mayasessiond`, Session Broker, or interactive desktop checks;
- `doctor`, `run`, `stop`, `status`, `attach`, screenshot, or recording behavior;
- Scenario parsing or execution, Fresh Run lifecycle, run retention, and kept sessions;
- Evidence Bundle layout, manifest, logs, Visual Evidence, and Review Comments;
- consuming-repo smoke wiring and docs that change the live proof contract.

## Merge Rule

Fake/local tests are still required, but fake-first tests do not satisfy
real-product proof. For `live_maya_required=true`, the live Maya gate must pass
against a configured real Windows Maya Host. Skipped, missing, or fake-only live
proof is a failure.

The live gate runs the desktop Visual Evidence proof first, then the older SSH
smokes:

```sh
go test ./internal/cli -run TestOptInRealVisualEvidenceSmoke -count=1
go test ./internal/cli -run 'TestOptInRealSSH(Doctor|Run|ConsumingRepo)Smoke' -count=1
```

That opt-in smoke runs `maya-stall doctor --scenario smoke`, then one real
`maya-stall run smoke` through `gg_mayasessiond`, and asserts the Evidence
Bundle, Scenario Result, logs, manifest, and real Visual Evidence bytes.
It also runs one canonical Consuming Repo Scenario from a checked-out consuming
repo path, then publishes the Evidence Bundle to a temporary filesystem
Evidence Store and verifies review-ready artifact files. The live Visual
Evidence smoke additionally asserts `maya.exe` is in the interactive Windows
`Console` session and stores a desktop screenshot plus short MP4 in its Evidence
Bundle.

Non-live-only changes may merge with local gates plus a manifest saying
`live_maya_required=false`.

## Repository Setup

Automation expects a protected GitHub Environment named `maya-live-proof`.
Configure required reviewers on that environment before adding live host
credentials, because the live job checks out and tests PR code after approval.

Add the live host config as an environment secret named
`MAYA_STALL_LIVE_HOST_CONFIG_B64`, containing base64-encoded host config YAML.
Add the canonical consuming repo checkout path as an environment secret named
`MAYA_STALL_LIVE_CONSUMING_REPO_SMOKE_DIR`. A variable with the same name is
accepted as fallback, but the secret keeps local runner paths masked in logs.
The self-hosted `maya-live-proof` runner must provide `python3 >= 3.10` with
`venv` and `ensurepip`; the live job checks this before running the consuming
repo local gate.
Optional environment or repository variables:

- `MAYA_STALL_LIVE_TARGET_PROFILE`
- `MAYA_STALL_LIVE_HOST`

Do not commit hostnames, SSH identities, credentials, generated live evidence,
or local machine paths. Public proof should report command classes, pass/fail
status, manifest status, and redacted live-config state only.

For live-required fork PRs, automation withholds live host credentials and fails
closed. A maintainer must review and promote the change to a trusted
same-repository branch or ref before the protected live Maya environment can run.
