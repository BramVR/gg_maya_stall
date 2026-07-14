# plan

`maya-stall plan` inspects a named Scenario without touching a Maya Host.

```sh
maya-stall plan smoke
maya-stall plan --json smoke
maya-stall plan --host-config ci-hosts.yaml smoke
```

## Behavior

The command loads Repo Run Config and uses the same Scenario normalization and
Run Payload manifest builder as `maya-stall run`. It reports each declared
payload's normalized source, staged destination, kind, byte size, SHA-256 hash,
and readiness. Missing and unsafe inputs are retained in the report with a
concrete reason.

For a directory declaration, `size` is the sum of regular-file bytes. Its hash
is a deterministic tree digest over each sorted repo-relative file path, a NUL
separator, the decimal byte size, another NUL separator, and the file bytes.
Path changes and content changes therefore both change the digest.

Requirements include the Scenario's Maya Version Requirement, the required
Session Broker, and known capabilities such as `script.execute`,
`screenshot.capture`, `recording.capture`, and `visual-evidence.required` for a
required Visual Evidence Validator.

When `--host-config` is present, `plan` reads every Target Profile, Host Pool,
and Maya Host from that local file. It reports compatible and incompatible
hosts using configured health, runtime shape, declared Maya inventory, and
Visual Evidence support. This is static planning information, not live health
proof; run `doctor` for live checks.

`plan` never acquires a Host Lock, creates Run State or an Evidence Bundle,
opens SSH, calls the Session Broker, reads an Evidence Store, or mutates an
external system. The final `host-contact: none` line makes that boundary
explicit in human output.

## JSON

`--json` emits one stable `scenario-plan` document. Its top-level fields are:

- `version`, `kind`, `scenario`, `configPath`, and `ready`;
- `requirements`;
- `payload` entries with `kind`, `source`, `destination`, `size`, `sha256`, and
  `status`;
- `issues` with source-specific reasons;
- `targetProfiles`, containing Host Pool and Maya Host compatibility.

The command exits `0` when local inputs are ready and, if host config was
provided, at least one configured Maya Host is compatible. It exits `1` for a
valid but blocked plan and `2` for command usage errors.
