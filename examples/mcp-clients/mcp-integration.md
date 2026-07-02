# MCP Integration

## Purpose

These examples show how to connect an MCP-capable client to the broker over stdio without assuming a single client-specific config format.

The broker MCP entrypoint is:

```bash
examples/mcp-clients/run_broker_mcp.sh
```

That wrapper:

- sets sane default broker paths under `.broker/`
- preserves override support through environment variables
- launches `broker/cmd/broker-mcp`
- works around the local `GOROOT` mismatch by unsetting it before `go run`

## Generic Stdio Definition

If your MCP client accepts a stdio server definition with `command`, `args`, and `env`, use the pattern in [generic-stdio-config.json](./generic-stdio-config.json).

Agent-oriented starter templates are also available:

- [Copilot CLI template](./copilot-cli.example.json)
- [Claude Code template](./claude-code.example.json)
- [Codex CLI template](./codex-cli.example.json)
- [Template notes](./client-config-templates.md)
- [Codex profile installer](./install_codex_profiles.sh)

Core values:

- command: `./examples/mcp-clients/run_broker_mcp.sh`
- args: `[]`
- env:
  - `BROKER_SLURM_MODE=stub` for local development
  - `BROKER_SLURM_MODE=command` when you want real Slurm submission

## Exposed Tools

The broker currently exposes these MCP tools:

- `rag_compress`
- `debug_with_local_context`
- `summarize_logs`
- `inspect_repo`
- `propose_patch`
- `submit_local_job`
- `submit_parallel_jobs`
- `get_job_status`
- `get_root_job_status`
- `retry_failed_root_shards`
- `release_deferred_root_chunks`
- `fetch_result`
- `get_retry_recommendation`
- `retry_with_recommended_profile`
- `fetch_job_logs`
- `cancel_job`
- `list_local_capabilities`

## Development Mode

For local or demo use:

```bash
BROKER_SLURM_MODE=stub examples/mcp-clients/run_broker_mcp.sh
```

That is the safest option when you want tool wiring without cluster interaction.

## Slurm Command Mode

For real scheduler-backed use:

```bash
BROKER_SLURM_MODE=command \
BROKER_SLURM_SCRIPT_PATH="$PWD/deploy/slurm/broker_worker.slurm" \
examples/mcp-clients/run_broker_mcp.sh
```

You will also need working `sbatch`, `sacct`, and `scancel` binaries in `PATH`.

## Notes

- This repo intentionally avoids claiming an exact config file format for a specific external MCP client unless that format is verified separately.
- The generic stdio shape here is meant to be adapted into the target client's MCP settings format.
- For direct HTTP demos instead of MCP, use `broker/cmd/broker-cli`.

Current observed client status:

- Codex CLI is verified against the current stdio MCP server, including `list_local_capabilities` and real `rag_compress` submission to Slurm.
- GitHub Copilot CLI still times out after `initialize` in this environment, so the Copilot template should be treated as a starting point rather than a verified integration.

## Codex Profiles

To keep the broker disabled by default and enable it only for selected Codex sessions, install the provided profiles:

```bash
examples/mcp-clients/install_codex_profiles.sh
```

That writes:

- `~/.codex/slurm-broker.config.toml`
- `~/.codex/local-broker.config.toml`

Use them explicitly:

```bash
codex -p slurm-broker
codex -p local-broker
```

The `slurm-broker` profile targets the Slurm-backed P40 tier.
The `local-broker` profile targets the local command backend on the current machine.

## Codex And llama.cpp

For direct Codex-to-llama.cpp integration on Slurm-backed local GPUs, the supported compatibility layer now lives under:

- `deploy/slurm/codex_llamacpp_proxy.py`
- `deploy/slurm/run_codex_llamacpp_proxy.sh`

The file in this examples directory is a compatibility shim that delegates to the supported deployment path.
