# Maya Stall Vision

Maya Stall is the release-qualification system for Autodesk Maya plug-ins. It turns a pool of owned Windows Maya Hosts into safe, schedulable test infrastructure. A developer, CI job, or trusted agent can submit a Maya Scenario, watch it run in the real Maya UI, review trustworthy evidence, and know that the Maya Host was safely returned to service afterward.

Maya Stall is not a generic remote command runner. It understands Maya sessions, Maya versions, plug-in compatibility, Windows desktops, visual evidence, scene outputs, and the cleanup rules needed for reliable UI testing.

## Product Promise

When Maya Stall reports a final passing result, it must be possible to prove:

- which Consuming Repo revision, local changes, Repo Run Config, Maya Scripts, scenes, and Plugin Artifacts ran;
- which compatible Windows Maya Host, Maya build, Session Broker, desktop, and capabilities were used;
- what happened at each Scenario step and what the user would have seen in Maya;
- which structured results, Validators, screenshots, recordings, scenes, logs, and measurements support the result;
- that the Maya UI Session was stopped and its run workspace was removed; and
- that the Maya Host was safely returned to the Host Pool.

A command exit code alone is not proof. Evidence, validation, ownership, and cleanup are part of the result.

## User Workflow

The normal workflow is simple:

1. The Consuming Repo defines a named Scenario, its files, required Maya Host capabilities, Expected Outputs, evidence policy, and Validators.
2. The user plans the run locally. Maya Stall shows what will be transferred, what the Scenario needs, and which Maya Hosts could run it without touching a host.
3. The user or CI submits the run. Maya Stall creates a Run ID before validation or remote checks so every accepted request has a durable record, including early failures.
4. The selected Target Profile identifies a Host Pool. The Control Plane selects or queues a healthy, compatible Maya Host from that pool and gives the run an exclusive Host Lock.
5. The Windows Host Agent creates a clean workspace and verifies the declared Run Payload. It asks the Session Broker to start and own a fresh interactive Maya UI Session and execute the Scenario.
6. Maya Stall streams named steps, logs, results, screenshots, recordings, Maya state, and diagnostics while the run is active.
7. Maya Stall collects Expected Outputs, runs Validators, and writes temporary evidence that will be finalized after cleanup.
8. The Stop Policy decides whether to clean up now or keep the session temporarily for debugging. A kept run is visible but not final.
9. For cleanup, the Windows Host Agent asks the Session Broker to stop the Maya UI Session, removes run-owned state, records the cleanup result, and releases the Host Lock.
10. Maya Stall then finalizes the verifiable Evidence Bundle and publishes the final review result. If cleanup failed, it publishes `cleanup-failed` instead and quarantines the Maya Host.

The terminal that submitted the run does not own its lifetime. A submitted run continues safely if the CLI disconnects, and the user can reconnect through its Run ID.

## Machine Pool And Parallel Work

Configured Maya Hosts are organized into Host Pools. A run's Target Profile selects a Host Pool, and the Control Plane chooses a compatible Maya Host from it. The Control Plane knows whether each Maya Host is online, healthy, ready, locked, running, kept for debugging, cleaning, under maintenance, unhealthy, or quarantined.

Runs may execute in parallel across different Maya Hosts. If five compatible Maya Hosts are ready, Maya Stall may run five independent Scenarios at the same time. Extra work waits in a visible queue. A release matrix may spread Maya versions, plug-in builds, renderers, GPUs, or Scenario groups across the Host Pool and combine their results into one qualification report.

Maya Hosts do not coordinate directly with each other. The Control Plane owns scheduling and run history. Each Windows Host Agent enforces the Host Lock, prepares workspaces, monitors the run, transfers evidence, and coordinates cleanup on its Maya Host. The Session Broker remains responsible for launching, owning, observing, and stopping the Maya UI Session.

## One Windows Desktop, One Execution Slot

One Maya Host represents one isolated interactive Windows desktop. Only one unrelated active or kept Maya UI run may use that Maya Host at a time.

A Maya Host may have several Maya versions installed, but Maya Stall selects one version and runs one UI Scenario at a time. This avoids conflicts in mouse and keyboard input, dialogs, screenshots, Maya preferences, trusted plug-in paths, licenses, memory, and GPU use.

More parallel capacity on one physical computer must come from real isolation, normally separate Windows virtual machines. Each isolated VM is configured as its own Maya Host with its own desktop, Windows Host Agent, workspace, capture path, and Maya process. Multiple user sessions or Maya processes do not count as separate Maya Hosts unless the Windows Host Agent can prove that their input, capture, files, processes, resources, and licensing are fully isolated.

A Kept Session continues to hold the Host Lock. The Maya Host cannot accept another run until the session is stopped or its deadline expires. A kept run may publish temporary debugging evidence, but it cannot receive a final passing result until cleanup finishes.

## Shared Ownership And Host Locks

The Host Lock is shared authority, not a repo-local file. It must be visible to every repo, worktree, CI runner, and operator that can use the same Maya Host.

When acquired, the Host Lock binds the exact Maya Host, Run ID, Consuming Repo, actor, and a unique lock token. After the Session Broker creates the Maya UI Session, Maya Stall records that exact session identity under the same Host Lock.

Every operation that can execute code, control the desktop, reuse a session, remove a workspace, stop Maya, or release a host must present and revalidate the current lock token and, when present, the recorded session identity.

Names, process IDs, paths, or old local state are never enough authority for a destructive action. A stale caller with an old lock token cannot control or clean a newer run. Manual adoption or reclaim is explicit, refuses conflicts, and is recorded as evidence.

## Run Lifecycle And Cleanup

An invalid command that does not identify a Scenario is only a usage error. Once a submission identifies its Consuming Repo and intended Scenario, Maya Stall creates a Run ID before Repo Run Config validation, host selection, or remote checks. That run finishes with a minimal Evidence Bundle even when execution never reaches a Maya Host. Its manifest says where the run failed, whether Visual Evidence capture began, and whether cleanup was needed and completed.

Every submitted run has durable state and ordered events. The lifecycle covers submission, queuing, locking, staging, launching, executing, validating, collecting, cleaning, publishing, and a final completed or failed result. Local planning is a separate read-only action and does not create a run.

Active runs send regular signs of life. Every Host Lock has an idle deadline and a hard lifetime. Kept Sessions have a visible expiry and may only be extended deliberately.

Cleanup is safe to retry. If a CLI, Control Plane, Windows Host Agent, Session Broker, Maya process, or Maya Host fails partway through a run, the remaining components compare the recorded state with the actual Maya Host state and finish recovery. A restarted Windows Host Agent inspects unfinished work before accepting anything new.

If cleanup cannot be proven, the run enters `cleanup-failed` and the Maya Host enters quarantine. It is not returned to the ready Host Pool. Operators can inspect the exact failure, retry cleanup, or perform an explicit recorded recovery.

A run is not fully complete until its required evidence is finalized and its cleanup result is known. A Kept Session is an intentional non-final state until it is stopped or expires.

## Fresh Maya Sessions And Debugging

Normal Scenarios start with a clean run workspace and fresh interactive Maya UI Session. Prepared hosts, installed Maya versions, safe caches, and trusted host-managed plug-in roots may remain between runs; run-owned processes and mutable workspace state do not.

On failure, Maya Stall collects temporary evidence before cleanup. The default policy then asks the Session Broker to stop Maya and returns the Maya Host to the Host Pool after cleanup is proven.

When a user deliberately keeps a failed session, the run remains inspectable through logs, events, current Maya and broker state, screenshots, and bounded run-scoped control. The user may stop it early or extend its deadline. Expiry guarantees that a forgotten debug session cannot reserve a Maya Host forever. After eventual cleanup, Maya Stall adds the cleanup result, finalizes the evidence, and closes the run.

## Scenario Contract

The consuming repo owns domain correctness. It defines the plug-in-specific Maya scripts, scenes, expected behavior, and assertions. Maya Stall supplies the safe execution and proof framework without encoding knowledge of a particular plug-in.

A Scenario may declare:

- exact or minimum Maya builds, Python compatibility, renderer or license needs, GPU needs, display needs, and required broker features;
- Plugin Artifacts, Maya Scripts, scenes, include paths, and other declared inputs;
- named steps, checkpoints, timeouts, expected outputs, evidence policy, and cleanup policy;
- structured assertions and measurements; and
- matrix dimensions such as Maya version, plug-in build, renderer, GPU class, or test group.

The optional Maya Python helper lets repo-owned scripts report steps, assertions, measurements, attachments, and Scenario Results in a stable format.

## Maya-Native Observation And Control

Maya Stall should understand the Maya application, not only pixels and coordinate clicks. Through the Session Broker and optional helper it can expose bounded, run-scoped observation of:

- Maya processes, versions, sessions, logs, errors, crashes, and hangs;
- loaded plug-ins, scenes, selections, DAG or dependency-graph state, nodes, and attributes;
- Maya and Qt windows, dialogs, widget identity, and accessibility state;
- viewport, camera, display, renderer, timing, memory, and GPU information; and
- scene open, save, reopen, plug-in load or unload, undo, redo, and other common proof points.

Actions should use stable Maya or Qt names and identities where possible. Coordinate input remains an explicit recovery tool that requires current Visual Evidence and the active Host Lock.

## Evidence Discipline

Every submitted run produces an Evidence Bundle. Runs that reach Maya include the required Visual Evidence, logs, structured Scenario Results, Validators, outputs, runtime metadata, and cleanup result. Earlier failures produce a minimal bundle with their events, failed layer, diagnostics, remediation hints, capture state, and cleanup state.

Visual Evidence comes from the real interactive Windows desktop and Maya UI Session used by the run. It is never faked or reconstructed after the fact. Capture metadata identifies its source, time, recorded Maya Host capabilities, and run.

The Evidence Bundle manifest is versioned and records a size and SHA-256 hash, a reliable file fingerprint, for every artifact in the complete bundle. It also records which safe subset was published. The manifest identifies the Maya Stall, Windows Host Agent, Session Broker, Maya, Repo Run Config, Run Payload, Consuming Repo revision, and recorded Maya Host capabilities that produced the result. Evidence can be verified offline. Optional signed receipts may prove that a manifest has not changed while making no stronger identity claim than the configured signing trust supports.

Evidence is private by default. Publication uses explicit file allowlists, access policy, expiry, retention, and size limits. Generated text diagnostics are redacted, but screenshots, scenes, logs, and binary outputs may still contain sensitive information and must remain subject to review policy.

## Review And Release Qualification

Maya Stall publishes results through normal development workflows. GitHub and GitLab checks show Scenario or matrix-cell status, failed steps, important evidence, cleanup state, and a durable link to the full Evidence Bundle or evidence timeline.

The Evidence view should make a failed UI run understandable without immediate reproduction. It combines steps, assertions, logs, screenshots, recordings, contact sheets, Maya state, output files, crash information, performance measurements, and cleanup status.

A release qualification may run a matrix across Maya versions, Windows builds, plug-in builds, renderers, GPUs, or Scenario groups. Required and advisory cells are explicit. Retries remain visible so flaky behavior is not hidden. Qualification can include scene round trips, visual baselines, crash and hang checks, memory limits, and performance budgets.

A review must not show a fully successful result while required evidence is missing or cleanup remains unresolved.

## Interfaces For People, CI, And Trusted Agents

The CLI, CI integration, SDK, and optional agent-facing protocol use the same Run IDs, Host Locks, permissions, events, and results. Machine-readable output is a first-class interface rather than a separate behavior.

Trusted agents may plan and submit runs, follow events, inspect Maya and evidence state, take additional screenshots, perform bounded run-scoped actions, and stop or extend Kept Sessions. Every change must be allowed for that agent, checked against the current Host Lock, and recorded. Agent support does not turn Maya Stall into a general unattended desktop-control product.

## Host Ownership And Installation Boundary

Maya Stall targets owned Windows Maya Hosts. The Windows Host Agent may install and update Maya Stall-owned components using signed packages. Maya, licenses, GPU drivers, renderers, studio tools, accounts, trusted plug-in locations, and base machine policy remain operator-managed.

Layered health checks diagnose local tools, Repo Run Config, host identity, connectivity, work roots, Windows Host Agent, Session Broker, interactive desktop, Maya build and license readiness, capture and control features, Host Locks, Scenario inputs, and Evidence Store access. Maya Stall points at the failing layer and does not silently mutate operator-managed prerequisites.

The normal data and control paths authenticate the exact host and service identity, use only configured credentials, and do not silently inherit extra credentials or access from the operator's machine. Credentials stay out of Repo Run Config, command arguments, logs, evidence, and review output.

## Trust Model

Maya Stall is for a cooperative trusted team operating owned infrastructure. Repo Run Config and Scenario code are trusted project automation. Maya Stall protects against accidental overlap, stale ownership, leaked sessions, unsafe cleanup, credential expansion, incomplete evidence, and ambiguous results. It is not a hostile multi-tenant sandbox or a security boundary between mutually untrusted users.

Host Credentials, private hostnames, broker credentials, Windows users, licenses, signing keys, and Evidence Store secrets live outside the consuming repo. Repo Run Config remains non-secret and portable.

## Product Boundaries

Maya Stall is not:

- a generic remote execution runner or CI replacement;
- a cloud-provider marketplace, spend manager, or arbitrary sandbox service;
- a tool for running multiple unisolated Maya UI tests on one Windows desktop;
- an installer for Autodesk Maya, licenses, drivers, renderers, or studio software;
- a secrets store or automatic scrubber for arbitrary captured pixels and files; or
- the owner of plug-in-domain correctness that belongs in the consuming repo.

The first product versions may use a smaller SSH-based path, one Session Broker, filesystem evidence, and a compact command set. Those are delivery steps, not the long-term product boundary. The enduring goal is trustworthy, parallel release qualification on real Maya UI Sessions with clear ownership, complete evidence, and verified cleanup.
