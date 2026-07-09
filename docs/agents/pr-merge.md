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

The live gate runs desktop Visual Evidence and desktop control proof first,
then the older SSH smokes, then the retained run-scoped desktop ops smoke last
because stopping a retained `gg_mayasessiond` run can tear down the broker
session. Live smokes restore the documented interactive sessiond UI scheduled
task before proof starts and retained-stop smokes restore it again before the
next proof step:

```sh
go test ./internal/cli -run TestOptInRealVisualEvidenceSmoke -count=1
go test ./internal/cli -run TestOptInRealDesktopControlModalSmoke -count=1
go test ./internal/cli -run TestOptInRealSSHDoctorSmoke -count=1
go test ./internal/cli -run TestOptInRealSSHConsumingRepoSmoke -count=1
go test ./internal/cli -run TestOptInRealSSHRunSmoke -count=1
go test ./internal/cli -run TestOptInRealRunScopedDesktopOpsSmoke -count=1
```

That opt-in smoke runs `maya-stall doctor --scenario smoke`, then one real
`maya-stall run smoke` through `gg_mayasessiond`, and asserts the Evidence
Bundle, Scenario Result, logs, manifest, and real Visual Evidence bytes.
It also runs one canonical Consuming Repo Scenario from a checked-out consuming
repo path, then publishes the Evidence Bundle to a temporary filesystem
Evidence Store and verifies review-ready artifact files. The live run smoke
uses a recording-enabled Scenario and validates its Evidence Bundle has a real
MP4 recording with duration/FPS and selected-host metadata. The live Visual
Evidence smoke additionally invokes the standalone `maya-stall record` command,
asserts `maya.exe` is in the interactive Windows `Console` session, and
validates the command's MP4 Evidence Bundle before publishing sanitized review
proof. The run-scoped desktop ops smoke keeps a failed run locked, proves
standalone screenshot fails closed while the Host Lock is held, captures a
run-scoped desktop screenshot, and clears a controlled modal with
`attach <run-id> control click`.

Non-live-only changes may merge with local gates plus a manifest saying
`live_maya_required=false`.

## Branch Protection

The live Maya proof gate is only non-skippable when GitHub branch protection
marks the proof checks as required on `main`. This cannot be verified from the
repository contents; confirm it against the GitHub API when auditing:

```sh
gh api repos/<owner>/<repo>/branches/main/protection \
  -q '{enforce_admins: .enforce_admins.enabled, contexts: .required_status_checks.contexts, allow_force_pushes: .allow_force_pushes.enabled, allow_deletions: .allow_deletions.enabled}'
```

Required configuration:

- Required status checks on `main`: `Proof Manifest, Local Gates`,
  `Live Maya Gate`, and `golangci-lint`.
- `enforce_admins` enabled, so the gate cannot be bypassed by direct pushes.
- No force pushes or branch deletion.

`Live Maya Gate` reports `skipped` for non-live changes, which satisfies the
required check; live-required changes wait on the `maya-live-proof` environment
approval, so an unapproved live PR cannot merge. Add the required contexts and
admin enforcement without replacing existing checks, review requirements, or
push restrictions:

```sh
gh api -X POST repos/<owner>/<repo>/branches/main/protection/required_status_checks/contexts --input - <<'JSON'
{
  "contexts": ["Proof Manifest, Local Gates", "Live Maya Gate", "golangci-lint"]
}
JSON
gh api -X POST repos/<owner>/<repo>/branches/main/protection/enforce_admins
```

These additive endpoints require branch protection to exist already. If the
audit reports force pushes or deletion as enabled, disable those settings in
the repository branch-protection rule without replacing its other controls.

Policy path coverage is audited automatically: `scripts/proof/audit-live-policy.mjs`
verifies every `proof/live-maya-policy.json` rule path still exists, every prefix
still matches tracked files, and every file under `cmd/`, `internal/`,
`scripts/proof/`, and `scripts/windows/` is covered by some rule. The audit runs
in the `Proof Script Tests` step on every PR; run it locally with
`node scripts/proof/audit-live-policy.mjs`. File drift it reports as follow-up
issues instead of weakening the policy.

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
- `MAYA_STALL_LIVE_PROOF_RETENTION_DAYS`, default `3`, max `14`
- `MAYA_STALL_LIVE_PROOF_PUBLIC_HOST_ALIAS`, default `maya-live-proof-host`

Do not commit hostnames, SSH identities, credentials, generated live evidence,
or local machine paths. Public proof should report command classes, pass/fail
status, manifest status, and redacted live-config state only.

## Live Visual Evidence Artifact

When the live gate passes, the workflow uploads a GitHub Actions artifact named
`live-visual-evidence-proof`. Reviewers can open the exact workflow run, choose
`Artifacts`, download `live-visual-evidence-proof`, and inspect:

- `proof-artifact-manifest.json`
- `evidence-metadata.json`
- `media-review.json`
- `screenshots/desktop-screenshot.png`
- `recordings/recording.mp4`

The proof artifact is generated only after the local Evidence Bundle exists and
only when the live workflow sets
`MAYA_STALL_LIVE_PROOF_ARTIFACT_ENABLED=true`. The manifest lists relative
paths, media types, byte sizes, SHA256 hashes, run id, Scenario, Target Profile,
the public-safe selected host alias, and retention days. The public artifact
confidentiality gate rejects proof text containing private host aliases, user
paths, SSH material, token-like fields, license variables, or symlinks before
upload. The gate also requires `media-review.json` to acknowledge that the
desktop PNG/MP4 came from a controlled public-proof desktop before those binary
files are uploaded.

S3/R2 publishing is deferred. A future backend should use a separate
`evidence store` config block with bucket endpoint, short lifecycle retention,
scoped write credentials from CI/user secrets only, and signed or otherwise
review-scoped reads. It must remain opt-in and must reuse the same sanitized
proof-artifact manifest and confidentiality gate before upload.

For live-required fork PRs, automation withholds live host credentials and fails
closed. A maintainer must review and promote the change to a trusted
same-repository branch or ref before the protected live Maya environment can run.
