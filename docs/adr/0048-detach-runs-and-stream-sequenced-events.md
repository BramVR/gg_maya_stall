# Detach Accepted Runs And Stream Sequenced Events

An accepted Configured Control Plane run belongs to the Control Plane, not to
the submitting HTTP connection. A failed response write or disconnected client
must not cancel, delete, or otherwise settle the run. A connected submitter may
continue waiting for the terminal record, while any later authenticated client
can reconnect by Run ID.

`GET /v1/runs/<run-id>/events?fromSequence=<n>&follow=true` streams bounded
newline-delimited records. Durable Run Ledger event sequence numbers are the
event identities in both live and historical reads. A reconnect starts at the
requested inclusive sequence, never renumbers an event, and never repeats or
reorders a sequence within one connection. `stream-end` identifies terminal
state. `stream-truncated` ends a connection at its byte ceiling and exposes the
next cursor so the client can reconnect.

Retention and connection truncation are response metadata, not synthetic event
identities. When retained history begins after the requested cursor, the stream
emits an `events-truncated` record with omitted-count and first-available cursor
before the retained events. A single oversized durable event may still be
replaced by the existing sequenced `run-ledger.event.truncated` event. Logs keep
their existing in-band truncation marker and response metadata.

Registered Windows Host Agents send bounded active Run Ledger checkpoints over
their credential-, process-session-, and Host-Lock-token-fenced assignment.
Checkpoints are capped at 10,000 events, 4 MiB of event bytes, and 4 MiB of log
bytes. The Control Plane preserves already acknowledged event identities and
payloads, merges the terminal transfer onto that prefix, and rejects stale or
conflicting checkpoints. Host-specific execution remains inside the Agent; the
Control Plane stores only the host-neutral Run Ledger contract.

Completed Run IDs remain available through history, events, logs, result,
Evidence metadata, and recorded cleanup state. Active evidence remains
unavailable until the Evidence Bundle is durable.

This extends [ADR 0045](0045-complete-a-fake-control-plane-scenario.md),
[ADR 0046](0046-complete-a-fake-scenario-through-a-registered-windows-host-agent.md),
and [ADR 0047](0047-complete-a-real-scenario-through-the-shared-path.md).
