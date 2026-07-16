# events

`maya-stall events` reads one run's durable ordered events from Embedded Mode
or a Configured Control Plane.

```sh
maya-stall events <run-id>
maya-stall events --json <run-id>
maya-stall events --control-plane https://maya-stall.example.com --json <run-id>
maya-stall events --control-plane https://maya-stall.example.com --from-sequence 42 <run-id>
```

JSON is a versioned object with `kind: events`, the Run ID, an `events` array,
and explicit truncation metadata. Every event preserves `sequence`,
`timestamp`, `phase`, `type`, `stream`, and structured `details`.

`--from-sequence` uses an inclusive positive cursor. Configured and embedded
reads return the same durable event identities. When the requested sequence has
already left bounded retention, `firstAvailableSequence`, `eventsOmitted`, and
`eventsTruncated` make the gap explicit. Use configured
[`attach`](attach.md) to follow an active run across bounded connections.
