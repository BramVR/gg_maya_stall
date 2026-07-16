# control-plane

`maya-stall control-plane serve` runs the authenticated HTTPS Control Plane.

Set `MAYA_STALL_CONTROL_PLANE_TOKEN` in the server environment, then run:

```sh
maya-stall control-plane serve \
  --listen 127.0.0.1:8443 \
  --data-dir /var/lib/maya-stall/control-plane \
  --tls-cert /etc/maya-stall/tls.crt \
  --tls-key /etc/maya-stall/tls.key
```

`--data-dir`, `--tls-cert`, and `--tls-key` are required. The listen address
defaults to `127.0.0.1:8443`. `--token-env` selects another environment
variable name; the token value is never accepted as a command argument or Repo
Run Config field. TLS certificate/key paths must be regular files, not
symlinks.

The server creates a missing data directory privately. It never changes an
existing directory's permissions; an existing root must already be private
(`0700` or stricter). Per-run state and the shared Host Lock namespaces are
private children.

## Enroll A Windows Host Agent

Generate a distinct high-entropy Agent credential of at least 32 bytes, expose
it to the enrollment client and Agent through the named environment variable,
then enroll the Agent's fixed Maya Host:

```sh
maya-stall control-plane enroll-agent \
  --control-plane https://maya-stall.example.com \
  --agent-id windows-agent-01 \
  --host maya-win-01 \
  --credential-env MAYA_STALL_HOST_AGENT_CREDENTIAL
```

Enrollment also uses the operator bearer token, from
`MAYA_STALL_CONTROL_PLANE_TOKEN` by default. `--token-env` selects another
operator-token variable. The Control Plane stores only the Agent credential's
SHA256 digest; neither credential is accepted through Repo Run Config or a
command argument.

After at least one Agent is enrolled, Scenario submissions require a registered
ready Agent process with a complete, fresh, compatible capability report for
the submitted Target Profile. Registration issues a leased, ephemeral process-session
fence; heartbeats renew it during execution, concurrent live processes using
the same Agent identity are rejected, and a replacement may take over only
after the prior lease expires and before execution confirmation. Loss after
confirmation quarantines the assignment. New assignments require an Agent that
advertises exact Maya UI Session binding during registration. Older version 1
Agents remain protocol-compatible for already-active assignments but are not
eligible for new work after reconnecting. The Control
Plane atomically persists the assignment and a unique
token-fenced Host Lock before making work visible. A Host has one slot. A
second assignment fails without changing the existing lock or assignment.
Restarted Control Planes reload active locks and keep their Hosts unavailable.
An unverified Maya shutdown moves the assignment to `quarantined`; its Agent
and shared Host Locks remain unavailable. Version 1 has no automated
quarantine recovery endpoint; this is an intentional fail-closed boundary.

Before an assignment or Host Lock is created, scheduling evaluates every ready
Host with the same compatibility decision used by `maya-stall plan`. Stale,
incomplete, offline, unhealthy, maintained, and quarantined reports never
qualify. Every mismatch is returned in the failed host-selection diagnostic.
Compatible Hosts in the Target Profile's Host Pool are selected by Maya Host id
and then Agent id, making selection deterministic. The selected report is
stored with the durable assignment. An exact or minimum Maya requirement also
resolves one concrete reported build; the Agent verifies that the fresh Maya UI
Session reports that build before Scenario execution.

Run the Agent with [`host-agent run-once`](host-agent.md). It makes only outbound
authenticated HTTPS requests, confirms the exact Host Lock token, binds the
launched Maya UI Session identity, executes one fake or Agent-configured real
Scenario, transfers bounded Run Ledger and Evidence files, cleans its run
workspace, and releases the shared Host Lock only after the Control Plane has
accepted durable terminal state.

Submissions are capped at 32 MiB including JSON/base64 expansion. The client
budgets the declared snapshot before reading payload files and rejects
symlinked Repo Run Config or payload paths, so configured mode does not upload
bytes outside the declared regular-file snapshot.

The version 1 API uses bearer authentication and origin-only HTTPS client URLs:

- `POST /v1/runs`
- `GET /v1/runs`
- `GET /v1/runs/<run-id>/status`
- `GET /v1/runs/<run-id>/events?fromSequence=<n>[&follow=true]`
- `GET /v1/runs/<run-id>/logs`
- `GET /v1/runs/<run-id>/result`
- `GET /v1/runs/<run-id>/evidence`
- `POST /v1/host-agents/enroll`
- `GET /v1/host-agents/<agent-id>/status`
- `POST /v1/host-agents/<agent-id>/register`
- `POST /v1/host-agents/<agent-id>/heartbeat`
- `POST /v1/host-agents/<agent-id>/assignments/next`
- `POST /v1/host-agents/<agent-id>/assignments/<run-id>/confirm`
- `POST /v1/host-agents/<agent-id>/assignments/<run-id>/session`
- `POST /v1/host-agents/<agent-id>/assignments/<run-id>/progress`
- `POST /v1/host-agents/<agent-id>/assignments/<run-id>/complete`
- `POST /v1/host-agents/<agent-id>/assignments/<run-id>/fail`

Submission returns newline-delimited versioned records on the same HTTPS
response. The first record returns the durable Run ID immediately after the
Run Ledger accepts it; the second returns the terminal result. The CLI
prints the same acceptance and terminal records with `run --json`. After
acceptance, execution belongs to the Control Plane: a failed response write or
submitter disconnect does not cancel or delete the run.

An authenticated client can reconnect with `attach <run-id> --control-plane
<url> --from-sequence <n>`. The follow response is bounded newline-delimited
JSON. Durable sequence numbers are identical in live and historical reads;
`events-truncated` exposes retention loss, `stream-truncated` exposes the next
connection cursor, and `stream-end` reports terminal state. History is newest
first and capped at 1,000 records with explicit truncation metadata.

With no Agent enrollment, the server preserves the original in-process fake
execution path. With an enrollment, a connected submitter may wait while the
registered Agent finishes the assignment. During execution, the Agent sends
bounded token-, session-, and Host-Lock-fenced Run Ledger checkpoints so a
later client can observe the same event identities before terminal transfer.
Registered Agent runs require
`--stop-after always`; policies that could retain a session fail before an
assignment is created. Real execution requires an Agent-local Host config;
submitting clients cannot send Host config or silently fall back to
embedded/direct-SSH ownership.

Completed Run IDs remain readable through history, events, logs, result,
Evidence metadata, and cleanup state. Active Evidence is unavailable until its
bundle is durable. Configured attach is observational; configured stop and
run-scoped desktop mutations remain later capabilities.
