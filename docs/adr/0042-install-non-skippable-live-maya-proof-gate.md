# Install Non-Skippable Live Maya Proof Gate

Maya Stall will use a checked-in changed-path policy and Proof Manifest to
decide whether a PR needs live Maya proof. For live product behavior changes,
CI fails closed unless a real Windows Maya Host smoke passes. Fake-first tests
remain the default development strategy and are still required, but they do not
prove real Session Broker, Host Health, Visual Evidence, or Fresh Run behavior.

The live gate runs `maya-stall doctor --scenario smoke` and one real
`maya-stall run smoke` through `gg_mayasessiond` via the opt-in live smoke test.
It must assert an interactive desktop Maya UI Session, not Windows Services
session `0`, and must assert an Evidence Bundle with Scenario Result, logs,
manifest, and real Visual Evidence bytes.

Non-live-only changes may pass with local gates and a manifest saying live Maya
proof is not required. Maintainers can change the live-required surface by
reviewing changes to `proof/live-maya-policy.json`.
