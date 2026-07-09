# Maya Stall Vision

Maya Stall makes real Autodesk Maya UI end-to-end tests something a consuming repo can own and trust, without moving host secrets into the repo or leaving Maya sessions behind. Core stays Maya-generic; the consuming repo owns domain correctness, and the Session Broker owns the Maya UI.

## Evidence Discipline

- A run must leave enough evidence to debug a UI failure later. Pass/fail alone is not a result; every run produces an Evidence Bundle with Visual Evidence, logs, and structured Scenario Results.
- Visual Evidence is captured through the Session Broker from the real interactive Maya UI Session, never faked or reconstructed after the fact.
- Published evidence is linkable from normal code review. A Review Comment points at durable Evidence Store artifacts, not at transient run state.

## Session Safety

- Fresh Runs are serialized per Maya Host with a Host Lock. No two active Fresh Runs share a host, so concurrent Maya UI sessions cannot corrupt each other.
- The Stop Policy decides cleanup explicitly. A run either stops its Maya UI Session or keeps it on purpose for debugging; a Kept Session is a deliberate choice, never a leak.
- Host selection fails closed. Maya Stall runs on the first healthy, unlocked host and treats failed health, lock, or inventory checks as a stop, not a guess.

## Ownership Boundaries

- Repo Run Config is non-secret. Host Pools, SSH keys, broker endpoints, Windows users, and license details live in Host Credentials outside `.maya-stall.yaml`, never in the consuming repo.
- The consuming repo owns domain correctness. Maya Stall stages only declared payload paths and runs repo-owned Maya Scripts; core ships generic Validators and never encodes plugin-domain logic.
- The Session Broker owns the Maya UI. Maya Stall asks for a session over a defined broker interface and stays out of how Maya is launched, attached, or captured.

## Owned Hosts, Diagnosed Not Installed

- Maya Stall targets owned Windows Maya Hosts over SSH. It is proof machinery for real interactive Maya, not a generic remote execution runner, a CI replacement, a secrets store, or a security boundary between untrusted users.
- Layered `doctor` checks diagnose host prerequisites — SSH, work root, Session Broker, Maya version, visual capture, Host Lock, Scenario inputs — and point at the failing layer instead of one vague readiness error. Maya Stall diagnoses hosts; it does not silently install or mutate them.
- The default test suite runs with fakes, no real Maya, hosts, or secrets. Real Maya behavior is proven behind a non-skippable live proof gate before it ships.
