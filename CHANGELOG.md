# Changelog

All notable user-facing changes for Maya Stall are recorded here.

Release history starts with `v0.1.0`.

## Unreleased

- Changed Repo Run Config and user host config decoding to reject unknown YAML fields while preserving standard merge-key anchors for shared SSH and broker defaults.
- Added host-config `trustedPluginArtifactsRoot` support so real SSH runs can copy declared Plugin Artifacts to a stable Maya trusted plug-in location while keeping clean per-run workspaces and repo-owned Scenario loading.
- Changed `maya-stall run` broker-failure handling to accept a collected passing Scenario Result only when configured Validators pass against collected outputs, while missing, malformed, failed, or Validator-failing results still fail non-zero.
- Added run-scoped `maya-stall attach <run-id> screenshot` and `maya-stall attach <run-id> control click` commands for modal debugging while the active or kept run owns the Host Lock.
- Added failure-time full-desktop screenshot capture for Fresh Runs that fail before Scenario Result collection when Scenario screenshot evidence is enabled and the selected Session Broker supports screenshot Visual Evidence.
- Added live proof coverage for full-desktop screenshot plus `maya-stall control click` against a controlled blocking desktop prompt on the Windows Maya Host.
- Added `maya-stall control click` for explicit full-desktop coordinate clicks through the selected Session Broker, using the same interactive Windows scheduled-task path as desktop Visual Evidence on real SSH hosts.
- Changed recording docs to describe the supported `maya-stall record` desktop MP4 path, local `ffmpeg` encoding, and the distinction between viewport capture and desktop Visual Evidence proof.
- Added Scenario-level recording Visual Evidence proof so `maya-stall run` captures MP4 recordings in the Evidence Bundle with duration/FPS, Target Profile, and selected Maya Host metadata, and the live proof gate validates a recording-enabled Scenario.
- Changed the live Visual Evidence proof gate to exercise the standalone `maya-stall record` command and validate its MP4 Evidence Bundle before publishing sanitized review proof.
- Changed `maya-stall doctor` Host Health so real `visual-evidence: ok` now proves broker viewport capture plus desktop recording capture/MP4 encoding readiness, with clear repair hints for missing `ffmpeg` or Windows desktop recording prerequisites.

## v0.1.0 - 2026-07-07

### Added

- Added real Windows Maya Host execution over SSH with a `gg_mayasessiond` Session Broker adapter, layered Host Health checks, opt-in live Maya Host smoke tests, and consuming-repo smoke proof: https://github.com/BramVR/gg_maya_stall/pull/27, https://github.com/BramVR/gg_maya_stall/pull/30, https://github.com/BramVR/gg_maya_stall/pull/31, https://github.com/BramVR/gg_maya_stall/pull/46, https://github.com/BramVR/gg_maya_stall/pull/52.
- Added real Visual Evidence and proof artifacts, including broker-captured screenshots, Windows desktop screenshot and MP4 proof capture, downloadable `live-visual-evidence-proof` artifacts, proof manifests, media review metadata, SHA256s, and a public artifact confidentiality gate: https://github.com/BramVR/gg_maya_stall/pull/23, https://github.com/BramVR/gg_maya_stall/pull/50, https://github.com/BramVR/gg_maya_stall/pull/62, https://github.com/BramVR/gg_maya_stall/pull/63.
- Added the core Scenario run pipeline: `maya-stall init`, fake end-to-end runs, Host Pools and Host Locks, typed Run Payload staging, Scenario Result validation, Fresh Run lifecycle, clean run workspace invariants, Stop Policy, kept sessions, `status`, `attach`, and `stop`: https://github.com/BramVR/gg_maya_stall/pull/16, https://github.com/BramVR/gg_maya_stall/pull/17, https://github.com/BramVR/gg_maya_stall/pull/18, https://github.com/BramVR/gg_maya_stall/pull/19, https://github.com/BramVR/gg_maya_stall/pull/20, https://github.com/BramVR/gg_maya_stall/pull/22, https://github.com/BramVR/gg_maya_stall/pull/47, https://github.com/BramVR/gg_maya_stall/pull/48, https://github.com/BramVR/gg_maya_stall/pull/49, https://github.com/BramVR/gg_maya_stall/pull/51.
- Added Evidence Bundle collection and publishing, filesystem Evidence Store support, and GitHub/GitLab Review Comment publishing over shared review-ready evidence metadata: https://github.com/BramVR/gg_maya_stall/pull/24, https://github.com/BramVR/gg_maya_stall/pull/25.
- Added a non-skippable live Maya proof workflow with checked-in proof policy, fail-closed live gate behavior, local/docs/artifact gates, and fork-safe live credential withholding: https://github.com/BramVR/gg_maya_stall/pull/44, https://github.com/BramVR/gg_maya_stall/pull/60.
- Added a tiny optional `maya_stall` Python helper for Maya Scenario scripts to write structured Scenario Result JSON: https://github.com/BramVR/gg_maya_stall/pull/21.
- Added first-user documentation: Windows Maya Host setup, Crabbox attribution, docs site checks, README hero/badges, command docs, concepts, source map, and a Windows host prepare script: https://github.com/BramVR/gg_maya_stall/pull/26, https://github.com/BramVR/gg_maya_stall/pull/28, https://github.com/BramVR/gg_maya_stall/pull/32, https://github.com/BramVR/gg_maya_stall/pull/33, https://github.com/BramVR/gg_maya_stall/pull/57, https://github.com/BramVR/gg_maya_stall/pull/58.
- Added first-release metadata, this changelog, and the release checklist: https://github.com/BramVR/gg_maya_stall/pull/64.

### Changed

- Tightened Host, Broker, Scenario, Fresh Run, Run Workspace, Evidence Bundle, and Visual Evidence module boundaries so v1 concepts stay host-neutral while Windows-specific behavior stays behind adapters: https://github.com/BramVR/gg_maya_stall/pull/45, https://github.com/BramVR/gg_maya_stall/pull/47, https://github.com/BramVR/gg_maya_stall/pull/48, https://github.com/BramVR/gg_maya_stall/pull/49, https://github.com/BramVR/gg_maya_stall/pull/50.
- Updated CI maintenance surfaces with the Go patch used by live proof and a golangci-lint gate: https://github.com/BramVR/gg_maya_stall/pull/53, https://github.com/BramVR/gg_maya_stall/pull/59.

### Dependency Freshness

- Dependency freshness after https://github.com/BramVR/gg_maya_stall/pull/63 found no safe or actionable Go module update. The only newer modules were reachable through the `gopkg.in/yaml.v3` test-only dependency chain, and patch refresh attempts were no-ops or churn.
