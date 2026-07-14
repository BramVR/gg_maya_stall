# Keep A Durable Embedded Run Ledger

Maya Stall will keep a bounded embedded Run Ledger under
`.maya-stall/state/ledger/runs/<run-id>/` for every accepted Run ID. The ledger
is durable across normal transient Run State cleanup and records run metadata,
terminal state, ordered structured events, and retained logs.

The embedded implementation is local and single-controller, but its command
and JSON contracts use the same Run ID and lifecycle vocabulary expected from a
future shared Control Plane. `history`, terminal `status --run`, and terminal
`attach` read this ledger. Kept runs continue to use truth-seeking Session
Broker status while their live session exists.

Repo Run Config may set ledger retention, maximum event count, maximum retained
event bytes, and maximum log bytes. Event and log limits use explicit
truncation markers. Automatic
retention removes only expired `completed` and `failed` ledger records; it does
not expire `submitted`, `kept`, or `cleanup-failed` records and never deletes
local or published Evidence Bundles.

This extends [ADR 0019](0019-store-run-state-separately-from-evidence-bundles.md)
from a two-way split to three responsibilities: transient operational Run
State, durable queryable Run Ledger, and review-ready Evidence Bundles. Host and
session cleanup remains governed by
[ADR 0033](0033-manage-run-retention-on-owned-hosts.md).
