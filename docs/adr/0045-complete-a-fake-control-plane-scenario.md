# Complete A Fake Scenario Through The Control Plane

Maya Stall will introduce shared mode with one thin fake-first vertical slice.
The CLI selects Configured Control Plane Mode through external flags and an
environment-sourced bearer token; Repo Run Config remains unchanged. Omitting
the Control Plane URL preserves Embedded Mode.

The first Control Plane is an authenticated, versioned HTTPS JSON service. It
accepts a safe snapshot of the selected Scenario's Repo Run Config and declared
Run Payload, creates the Run ID, and drives the existing fake Fresh Run
lifecycle. Each run has a private server-owned workspace containing the same
durable Run Ledger and Evidence Bundle contracts used in embedded mode.

Status, ordered events, bounded logs, result, and Evidence Bundle metadata are
read by Run ID. JSON responses are versioned; human CLI output renders the same
responses. Final success requires a passing Scenario, `completed` lifecycle
state, finalized evidence, and completed cleanup. A `kept` or
`cleanup-failed` run is never a final success.

This slice is synchronous and fake-only. It does not register a Windows Host
Agent, schedule shared Maya Hosts, execute direct SSH from the Control Plane, or
claim reconnectable live streaming. Those capabilities remain later shared
path slices.

This extends [ADR 0044](0044-keep-a-durable-embedded-run-ledger.md) by using the
same public Run ID and read contracts in both operating modes.
