# init

`maya-stall init` writes a repo-only sample `.maya-stall.yaml`.

```sh
maya-stall init
```

The generated config is safe to commit. It demonstrates a named Scenario,
typed Run Payload paths, Expected Outputs, Visual Evidence policy, and generic
Validators. It does not include Host Pools, Host Credentials, private
hostnames, SSH keys, Windows users, license details, or Evidence Store paths.

Run the generated Scenario:

```sh
maya-stall doctor --scenario smoke
maya-stall run smoke
```

If a repo already has config, inspect the file before overwriting it.
