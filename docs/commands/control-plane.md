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
(`0700` or stricter). Per-run state and the shared fake-host lock namespace are
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
ready Agent process. Registration issues a leased, ephemeral process-session
fence; heartbeats renew it during execution, concurrent live processes using
the same Agent identity are rejected, and a replacement may take over only
after the prior lease expires and before execution confirmation. Loss after
confirmation quarantines the assignment. The Control
Plane atomically persists the assignment and a unique
token-fenced Host Lock before making work visible. A Host has one slot. A
second assignment fails without changing the existing lock or assignment.
Restarted Control Planes reload active locks and keep their Hosts unavailable.
An unverified Maya shutdown moves the assignment to `quarantined`; its Agent
and shared fake-host locks remain unavailable. Version 1 has no automated
quarantine recovery endpoint; this is an intentional fail-closed boundary.

Run the Agent with [`host-agent run-once`](host-agent.md). It makes only outbound
authenticated HTTPS requests, confirms the exact Host Lock token, executes one
fake Scenario, transfers bounded Run Ledger and Evidence files, cleans its run
workspace, and releases the shared Host Lock only after the Control Plane has
accepted durable terminal state.

Submissions are capped at 32 MiB including JSON/base64 expansion. The client
budgets the declared snapshot before reading payload files and rejects
symlinked Repo Run Config or payload paths, so configured mode does not upload
bytes outside the declared regular-file snapshot.

The version 1 API uses bearer authentication and origin-only HTTPS client URLs:

- `POST /v1/runs`
- `GET /v1/runs/<run-id>/status`
- `GET /v1/runs/<run-id>/events`
- `GET /v1/runs/<run-id>/logs`
- `GET /v1/runs/<run-id>/result`
- `GET /v1/runs/<run-id>/evidence`
- `POST /v1/host-agents/enroll`
- `GET /v1/host-agents/<agent-id>/status`
- `POST /v1/host-agents/<agent-id>/register`
- `POST /v1/host-agents/<agent-id>/heartbeat`
- `POST /v1/host-agents/<agent-id>/assignments/next`
- `POST /v1/host-agents/<agent-id>/assignments/<run-id>/confirm`
- `POST /v1/host-agents/<agent-id>/assignments/<run-id>/complete`
- `POST /v1/host-agents/<agent-id>/assignments/<run-id>/fail`

Submission returns newline-delimited versioned records on the same HTTPS
response. The first record returns the durable Run ID immediately after the
Run Ledger accepts it; the second returns the terminal fake result. The CLI
prints the same acceptance and terminal records with `run --json`.

With no Agent enrollment, the server preserves the original in-process fake
execution path. With an enrollment, it waits synchronously for the registered
Agent to finish the fake assignment. Registered Agent runs require
`--stop-after always`; policies that could retain a session fail before an
assignment is created. Real Maya execution through the Agent is not implemented
by this slice.

Kept runs remain readable and are never reported as final success, but this
first slice has no configured-mode attach or stop mutation. It therefore omits
embedded-only follow-up commands. Remote active-run control and cleanup are a
later Control Plane capability.
