# host-agent

`maya-stall host-agent run-once` registers one outbound Windows Host Agent,
waits for one assignment, completes its Scenario, transfers durable state, and
exits. Without `--host-config` it preserves the explicit fake development path.
With `--host-config` it requires the assigned Target Profile and Maya Host to
resolve to the live `ssh-sessiond` runtime; invalid, mismatched, or fake config
fails closed.

```sh
maya-stall host-agent run-once \
  --control-plane https://maya-stall.example.com \
  --agent-id windows-agent-01 \
  --host maya-win-01 \
  --work-root C:/maya-stall/agent \
  --host-config C:/maya-stall/config/hosts.yaml \
  --credential-env MAYA_STALL_HOST_AGENT_CREDENTIAL
```

The Agent must match an existing `control-plane enroll-agent` record. Its
credential must contain at least 32 bytes and comes only from
`--credential-env`; the value is never a command argument or Repo Run Config
field. The Control Plane URL must be an origin-only HTTPS URL.

`--work-root` must be a private Agent-owned directory. On POSIX systems the CLI
enforces `0700` or stricter permissions. On Windows it replaces inherited ACLs
with full control for the current owner, SYSTEM, and Administrators, then
verifies that no other identity has an allow rule. Filesystem and share roots
are rejected before ACL mutation. The Agent creates a clean per-assignment
workspace there; its repo snapshot and fake Host Lock namespace are
assignment-scoped. Real Host Credentials, Host Pools, Session Broker settings,
and Maya Host paths come only from the Agent-local `--host-config`; they are not
accepted from Repo Run Config or transferred through the Control Plane. The
Agent snapshots the validated config into its private per-run workspace, so a
later replacement of the operator path cannot change the selected runtime.

Agent and enrolled Host IDs are portable durable-state keys: 1-63 lowercase
ASCII letters, digits, and interior hyphens, excluding Windows reserved device
names. This prevents case, trailing-dot, and device-name aliases from sharing a
state path on Windows.

This command advertises exactly one slot. It confirms the unique Host Lock token
and its leased process-session fence before execution, renews that fence while
working, and binds the shared Host Lock to the exact Session Broker adapter and
Maya UI Session identity immediately after launch and before payload staging.
Registration explicitly advertises this binding capability. The Control Plane
does not assign new work to an older Agent that omits it; an assignment already
in flight during a rolling upgrade may finish under its original contract.
That binding is token- and process-session-fenced, durable across Control Plane
restart, and must match the transferred Evidence Bundle. The Agent runs only
the assigned Scenario and declared snapshot and
uploads only the run's bounded ledger and Evidence Bundle. Empty long-poll
responses are retried until one assignment arrives. The complete per-run Agent
repo is removed before completion is sent, so it is absent when the Control
Plane releases the shared Host Lock. Post-confirmation setup failures use the
same token fence to finalize a failed run and release the slot. Unauthorized
credentials and stale tokens are rejected without mutating durable state.

If Maya session shutdown cannot be verified, the Agent retains its workspace
and reports the assignment as `quarantined`. The Control Plane keeps the Agent
Host Lock and shared fake-host lock unavailable and rejects process takeover
after the process exits. This fake-first slice intentionally provides no
automated quarantine recovery command; the fail-closed state requires operator
inspection outside Maya Stall before the Control Plane data is replaced.

The fake Agent path accepts only cleanup-guaranteed `--stop-after always`
submissions. After its one completion or reported failure, the Agent becomes
offline and the command exits; another run requires a new `run-once` process.
If a process disappears, a replacement can take over its durable assignment
after the prior session lease expires only before execution confirmation. Loss
after confirmation quarantines the assignment because shutdown is unverified.

The Agent can execute one real Maya Scenario through its configured Session
Broker. It is still a one-assignment command, not a reconnecting background
service.
