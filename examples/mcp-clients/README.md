# MCP Client Examples

This directory should contain example MCP client configurations for:

- GitHub Copilot CLI
- Claude Code
- Codex CLI
- local smoke/demo flows

Current broker-facing binaries:

- `broker/cmd/broker-mcp` for stdio MCP integration
- `broker/cmd/broker-cli` for direct HTTP demos and operator flows

Current examples should target the implemented broker tools:

- `submit_local_job`
- `get_job_status`
- `fetch_result`
- `fetch_job_logs`
- `cancel_job`
- `list_local_capabilities`

Current examples:

- [Command-mode smoke demo](./command-mode-smoke.md)
- [Generic MCP integration notes](./mcp-integration.md)
- [Generic stdio config shape](./generic-stdio-config.json)
- [Client config templates](./client-config-templates.md)
- `run_broker_mcp.sh` for local stdio launch
- `codex_llamacpp_proxy.py` compatibility shim that forwards to the supported deployment asset in `deploy/slurm/`
- `copilot-cli.example.json`
- `claude-code.example.json`
- `codex-cli.example.json`

What is still missing:

- end-to-end examples that show a full submit, poll, fetch, and optional log-debug flow
