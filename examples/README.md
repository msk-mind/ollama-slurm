# Examples

User-facing integration examples belong here.

Current focus:

- `mcp-clients/` for agent configuration examples
- `mcp-clients/install_codex_profiles.sh` for opt-in Codex profile installation
- `mcp-clients/codex-profiles/` for reusable Codex profile templates

The broker MCP surface already exists under `broker/cmd/broker-mcp`, so this directory should now be treated as the place for:

- MCP client configuration examples
- smoke-test flows for broker tool usage
- end-user walkthroughs for interactive and background delegation

Near-term examples should cover:

- generic stdio MCP wiring
- GitHub Copilot CLI integration
- Claude Code integration
- Codex CLI integration
- direct broker CLI flows for environments where MCP is not yet wired in
