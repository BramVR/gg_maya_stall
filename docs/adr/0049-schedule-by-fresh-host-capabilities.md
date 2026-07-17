# Schedule By Fresh Maya Host Capabilities

Registered Windows Host Agents report one versioned Maya Host capability record
at registration and refresh it with each process-session heartbeat. Version 2
records contain the report time, online/health/maintenance/quarantine state,
Target Profile membership with its Host Pool mapping, Maya builds, Python, Session Broker version and
features, capture and control features, renderers, GPU, display, licensing, and
trusted Plugin Artifact support.

A record qualifies only when every category was reported, the Host is online
and healthy, it is neither under maintenance nor quarantined, and its timestamp
is no more than one minute old. A small bounded forward-clock tolerance avoids
rejecting healthy distributed Agents without letting a report extend its
freshness materially. Missing categories differ from explicitly empty feature
lists: missing is incomplete; empty means the Host reports no support.

Scenarios may require exact or minimum Maya, Python, and Session Broker
versions, required Session Broker/capture/control/renderer/GPU/display/licensing
values, and a required trusted Plugin Artifact boolean. The legacy
`mayaVersion` field remains an exact Maya requirement, but cannot be combined
with `requirements.maya`. Every Scenario implicitly requires
`script.execute`; enabled screenshot and recording evidence imply their
matching capture capabilities.

Local planning converts each Host config entry into a current capability record
and calls the same compatibility decision as Control Plane scheduling. The
configured scheduler uses only fresh Agent-reported records, explains every
mismatch, and filters candidates to the requested Target Profile before Host
Lock acquisition. Compatible Hosts are ordered by Maya Host id, with Agent id
only as a tie-breaker, so map iteration and connection order cannot affect the
selection. The exact report used for selection is copied into the durable
assignment. Exact or minimum Maya matching selects the report's declared
Session Broker build rather than another installed build. The Agent durably
binds the fresh Maya UI Session first, then verifies that build before payload
staging or Scenario execution. Conflicting Host
definitions that reuse one Host id across Target Profile pools are rejected.

Version 2 makes the Target Profile-to-Host Pool mapping mandatory for new work.
The Control Plane rejects version 1 registration and heartbeat reports before
status mutation; operators must upgrade the Agent and Control Plane together.
No legacy report is reinterpreted by guessing a Host Pool.

This extends [ADR 0013](0013-select-first-healthy-unlocked-host.md),
[ADR 0014](0014-use-layered-host-health-checks.md),
[ADR 0025](0025-declare-maya-version-requirements.md), and
[ADR 0046](0046-complete-a-fake-scenario-through-a-registered-windows-host-agent.md).
