# Complete A Real Scenario Through The Shared Path

The registered Windows Host Agent may receive an operator-owned Host config
through `host-agent run-once --host-config`. The config stays outside Repo Run
Config and the Control Plane submission. It must select the assigned Maya Host
in the submitted Target Profile and resolve to the live-proof-eligible
`ssh-sessiond` runtime. Invalid, mismatched, or fake configuration fails closed;
the Agent never replaces it with generated fake config. Validation and
execution use one private per-run snapshot, preventing an operator-path change
between runtime validation and selection.

After the Agent confirms the shared Host Lock and the Session Broker launches a
fresh Maya UI Session, the Agent binds that exact broker adapter and session ID
to the existing shared Host Lock before payload staging. The binding requires
the current lock token, Agent credential, and process-session fence, is written
through the assignment transition journal, survives Control Plane restart, and
must match the transferred Evidence Bundle. A conflicting identity is rejected
without mutation. The Agent advertises binding support at registration; only a
capable Agent receives a new assignment. Legacy version 1 Agents can still
finish an assignment that was already active during an upgrade.

The submitting CLI remains a Control Plane client: configured mode rejects
client Host config and cannot fall back to repo-local or direct-SSH ownership.
The Agent owns real-host execution, the Session Broker owns the interactive Maya
UI Session, and the Control Plane owns shared admission and terminal records.
The Agent removes its run workspace before asking the Control Plane to release
the shared Host Lock. The Fresh Run must first stop the broker session and
remove its remote run workspace; uncertainty quarantines the assignment.

Protected live proof submits one Scenario through the full Control Plane to
registered Agent path, inspects the bound Host Lock while Maya is active, then
requires real Visual Evidence, Scenario Result, logs, live runtime metadata,
Target Profile and Maya Host identity, explicit inactive broker state, and no
Agent workspace, remote workspace, shared lock, or host-side lock residue.

This extends [ADR 0046](0046-complete-a-fake-scenario-through-a-registered-windows-host-agent.md).
The explicit no-Host-config fake Agent path remains for development and default
tests. A reconnecting Agent service and automated quarantine recovery remain
later work.
