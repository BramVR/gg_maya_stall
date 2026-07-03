# AGENTS.md

## Agent skills

### Issue tracker
Issues and PRDs for this repo live in GitHub Issues. External PRs are not a triage request surface by default. See `docs/agents/issue-tracker.md`.

### Triage labels
Use the default Matt Pocock skill labels: `needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, and `wontfix`. See `docs/agents/triage-labels.md`.

### Domain docs
Single-context repo: root `CONTEXT.md` plus ADRs in `docs/adr/`. See `docs/agents/domain.md`.

### Windows Maya dev host
Use `docs/agents/windows-maya-host.md` before changing live SSH, `gg_mayasessiond`, broker, screenshot, recording, or opt-in smoke behavior. The current Bram dev fixture is `desktop-mtiofol`.
