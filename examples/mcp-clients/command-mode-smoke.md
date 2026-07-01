# Command-Mode Smoke Demo

This repository includes a fake Slurm harness that exercises the broker's real command-mode adapter without requiring a cluster.

Run:

```bash
tests/e2e/smoke_command_mode.sh
```

What it does:

- creates fake `sbatch`, `sacct`, `scancel`, and `squeue` commands
- starts the broker in `command` mode against those commands
- submits a `document_summary` job via `broker-cli`
- waits for the worker to complete
- fetches the broker-ingested result

This is useful for validating the broker control plane and worker integration before testing against a real Slurm environment.
