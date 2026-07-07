# Changelog

All notable user-facing changes for Maya Stall are recorded here.

Release history starts with `v0.1.0`.

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
