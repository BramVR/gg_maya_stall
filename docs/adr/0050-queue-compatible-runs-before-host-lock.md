# Queue Compatible Runs Before Host Lock

A Configured Control Plane Run that has at least one fresh, healthy, eligible,
compatible Maya Host may wait durably when every such Host is busy. A Run with
no currently compatible Host still fails host selection. Offline, stale,
unhealthy, maintained, quarantined, incomplete, or incompatible capability
records never establish queue admission or qualify for assignment.

Queue order is deterministic FIFO by durable acceptance timestamp, with Run ID
as the tie-breaker. Hosts are considered by Host ID, then Agent ID. Each free
Host takes the first compatible, non-dispatching Run in queue order. This lets
later Runs use otherwise-idle Hosts without bypassing an earlier Run that the
same Host could execute. Compatibility and Target Profile membership are
reevaluated at dispatch, so capability loss or Host loss leaves the Run queued
until safe capacity returns.

The Control Plane writes a private `admitting` queue intent before Run
acceptance, then advances it to `queued` after the submitted ledger is durable.
Restart recovery cleans a pre-ledger intent or completes a submitted-ledger
transition idempotently and emits
one `run.queued` identity with the truthful `awaiting-host-assignment` detail,
including when assignment is immediate. The queue record remains until assignment and Host
Lock ownership are durable; a stale record beside an existing assignment is
removed during recovery. The Control Plane transfers the accepted/queued event
prefix with the assignment so Agent checkpoints continue the same sequence.

Status derives one-based queue position, Host Pool, normalized requirements,
and wait reason from durable queue state. The durable queue admits at most
1,000 Runs; overflow fails before acceptance with no Run state. Queue status
and event surfaces remain bounded. Host Pool is copied from the Agent-reported
Target Profile mapping; it is never inferred from the Target Profile name.
Operator cancellation is allowed only before dispatch, records
`run.canceled` plus minimal Evidence, and touches no Maya Host or Host Lock.
Once dispatch is marked, cancellation fails closed and the normal assignment
cleanup contract owns the Run. Cancellation writes a private `canceling` intent
before terminal evidence; restart completes that intent idempotently so a
partially written cancellation cannot return to dispatch.

This extends [ADR 0011](0011-serialize-fresh-runs-per-maya-host.md),
[ADR 0046](0046-complete-a-fake-scenario-through-a-registered-windows-host-agent.md),
[ADR 0048](0048-detach-runs-and-stream-sequenced-events.md), and
[ADR 0049](0049-schedule-by-fresh-host-capabilities.md).
