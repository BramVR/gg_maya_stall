# AGENTS.md

## Agent skills

### Commits
Use `committer` for commits in this repo. Stage only the intended files.

### Issue tracker
Issues and PRDs for this repo live in GitHub Issues. External PRs are not a triage request surface by default. See `docs/agents/issue-tracker.md`.

### Triage labels
Use the default Matt Pocock skill labels: `needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, and `wontfix`. See `docs/agents/triage-labels.md`.

### Domain docs
Single-context repo: root `CONTEXT.md` plus ADRs in `docs/adr/`. See `docs/agents/domain.md`.

### Windows Maya dev host
Use `docs/agents/windows-maya-host.md` before changing live SSH, `gg_mayasessiond`, broker, screenshot, recording, or opt-in smoke behavior. The current Bram dev fixture is `desktop-mtiofol`.

## Project Structure & Module Organization

Maya Stall is a Go CLI plus an optional Python helper for Maya scripts. The CLI entrypoint is `cmd/maya-stall`, with implementation and Go tests in `internal/cli`. Python helper source lives in `helpers/python/maya_stall`, with tests in `helpers/python/tests`. Documentation lives in `docs/`; command docs are under `docs/commands`, setup runbooks under `docs/setup`, agent routing notes under `docs/agents`, ADRs under `docs/adr`, and PRDs under `docs/prd`. Generated outputs such as `bin/`, `dist/`, `.maya-stall/state/`, and `artifacts/maya-stall/` should not be edited by hand.

## Product Positioning

Maya Stall is a real Autodesk Maya UI end-to-end testing tool for owned Windows Maya Hosts. New code, docs, tests, and examples should not mention `gg_klv_push`, private hostnames, Bram-only workflows, or other project/person-specific workflows unless the file is explicitly about the current development fixture or release history. Prefer neutral examples such as `example-org`, `alice@example.com`, `owner/repo`, `smoke`, `maya-win-01`, and generic consuming repo workflows.

## Architecture Boundaries

Keep core Maya Stall concepts host-neutral where practical. Core may pass Repo Run Config, Target Profile, Host Pool, Maya Host, Scenario, Run Payload, Evidence Bundle, and Review Comment context, and may call transport/session/evidence capabilities for defaults, staging, diagnostics, run execution, cleanup, and publishing. Host-specific SSH, Windows desktop, Session Broker, filesystem, review-platform, and Visual Evidence semantics live behind focused adapters or helpers. No hard-coded dev fixture paths, machine names, user names, Maya install paths, or `gg_mayasessiond` assumptions in core unless the file is explicitly fixture documentation or unavoidable routing/config glue.

## Build, Test, and Development Commands

- `go build -trimpath -o bin/maya-stall ./cmd/maya-stall`: build the local CLI.
- `go vet ./...`: run Go static checks.
- `go test -race ./...`: run the Go test suite with the race detector.
- `go test ./...`: run the normal Go test suite.
- `gofmt -w $(git ls-files '*.go')`: format Go files.
- `python -m pytest helpers/python/tests`: run Python helper tests when pytest is available.
- `MAYA_STALL_SMOKE_HOST_CONFIG=/path/to/ci-hosts.yaml go test ./internal/cli -run TestOptInRealSSHDoctorSmoke -count=1`: opt in to live SSH smoke only with explicit host config.

## Coding Style & Naming Conventions

Use standard Go formatting and keep package names short and lowercase. Prefer table-driven Go tests where behavior has multiple cases, and keep command behavior close to the matching file in `internal/cli` (for example, review comment behavior in `review_comment.go`). Python helper code should stay small, dependency-light, and compatible with Maya-owned Python environments.

## Testing Guidelines

Name Go tests `*_test.go` beside the code they cover. Name Python helper tests `test_*.py` under `helpers/python/tests`. Add regression tests for bug fixes when practical. Default tests must not require Autodesk Maya, private hosts, live SSH, credentials, or an Evidence Store. Before handoff, run the relevant subset; before release or broad changes, run the full CI-equivalent gate from the README.

## Commit & Pull Request Guidelines

History uses Conventional Commit prefixes such as `feat:`, `fix:`, `docs:`, and `ci:`. Keep commits focused and mention user-visible behavior changes. Use `committer` and stage only intended files. Pull requests should include a clear summary, verification commands, config or secret implications, and screenshots only for generated docs or UI changes. Issue/PR references: always use full GitHub URLs, every time.

## Security & Configuration Tips

Keep Host Credentials, SSH keys, Windows credentials, private hostnames, license details, Evidence Store secrets, GitHub/GitLab tokens, and Session Broker secrets out of the repository. Do not pass secrets as command-line arguments. Repo config belongs in `.maya-stall.yaml` or `maya-stall.yaml` and must stay non-secret. Host config belongs in user config, CI secrets, runner-owned files, or operator-managed paths passed with `--host-config`. Review Comments read tokens from exact environment variables such as `GITHUB_TOKEN`, `GITLAB_TOKEN`, or `--token-env`; do not persist token values in docs, fixtures, logs, or generated evidence.
