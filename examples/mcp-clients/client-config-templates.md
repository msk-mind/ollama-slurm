# Client Config Templates

This directory now includes starter templates for three MCP-capable agent surfaces:

- `copilot-cli.example.json`
- `claude-code.example.json`
- `codex-cli.example.json`

These files intentionally use the same stdio pattern:

- command: `./examples/mcp-clients/run_broker_mcp.sh`
- no positional arguments
- environment overrides for local broker behavior

## Why They Are Templates

This repository owns the broker, not the external client configuration format.

That means these files should be treated as:

- concrete starting points
- examples of the expected stdio wiring
- easy-to-adapt snippets for whichever MCP settings surface your client exposes

You may need to rename the top-level key, move the JSON into a broader settings document, or adapt path resolution to match your client.

## Local Development Mode

For initial setup, keep:

```json
{
  "BROKER_SLURM_MODE": "stub"
}
```

That exercises the MCP server wiring without requiring a real scheduler.

## Real Slurm Mode

When moving to scheduler-backed execution, adapt the `env` section to include values such as:

```json
{
  "BROKER_SLURM_MODE": "command",
  "BROKER_SLURM_SCRIPT_PATH": "/absolute/path/to/deploy/slurm/broker_worker.slurm",
  "BROKER_REPO_ROOT_PATH": "/absolute/path/to/repo"
}
```

Your shell environment or wrapper script must also expose working `sbatch`, `sacct`, and `scancel` commands.
