# Vendor Useful Crabbox Code

Maya Stall may selectively copy or vendor useful Crabbox code because Crabbox is MIT licensed and its implementation already solves adjacent transport, config, evidence, and Windows desktop problems. Vendored or copied code must keep required MIT license attribution, stay limited to modules that serve the Maya Stall v1 workflow, and be adapted around Maya-specific concepts rather than turning Maya Stall into a generic Crabbox fork.

## Copied Or Adapted Surface

- Source: `openclaw/crabbox` at commit `5c58c806`, MIT licensed, with notice in `THIRD_PARTY_NOTICES.md`.
- Adapted in code: normal recording defaults from upstream `openclaw/crabbox:internal/cli/artifacts.go`, currently `10s` at `15fps`.
- Documented only: proof/failure clip, screenshot settle, kept-session TTL, and idle-timeout defaults from `docs/prd/0001-maya-stall-v1.md` and Crabbox default config. These are not broad-vendored until Maya Stall needs the behavior.
- Adapted: artifact URL and Review Comment markdown patterns from upstream `openclaw/crabbox:internal/cli/artifacts_publish.go`, rewritten around Evidence Bundles and Review Comments.
- Adapted: safe artifact/path handling patterns from upstream `openclaw/crabbox:internal/cli/run_artifacts.go`, rewritten around Scenario Run Payloads and Evidence Bundles.
- Not vendored: Crabbox providers, cloud leases, coordinator, WebVNC portal, generic run profiles, or provider-specific artifact upload backends.
