# control-plane

`maya-stall control-plane serve` runs the first authenticated HTTPS Control
Plane.

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
private children. A kept fake run retains that server-wide Host Lock and blocks
later runs from reusing the slot.

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

Submission returns newline-delimited versioned records on the same HTTPS
response. The first record returns the durable Run ID immediately after the
Run Ledger accepts it; the second returns the terminal fake result. The CLI
prints the same acceptance and terminal records with `run --json`.

This first server executes fake Scenarios synchronously. It does not register a
Windows Host Agent or run real Maya through shared mode.

Kept runs remain readable and are never reported as final success, but this
first slice has no configured-mode attach or stop mutation. It therefore omits
embedded-only follow-up commands. Remote active-run control and cleanup are a
later Control Plane capability.
