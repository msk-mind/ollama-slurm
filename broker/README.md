# Broker

Control-plane code for the local AI compute broker lives here.

Current responsibilities:

- MCP server implementation
- broker HTTP API
- Slurm backend adapter
- local direct-execution backend adapter
- policy enforcement
- cache lookup and reuse
- schema validation and result ingestion
- audit logging and integrity verification
- authn and per-job authz checks

Current layout:

- `cmd/broker-server/`: broker service entrypoint
- `cmd/broker-mcp/`: stdio MCP server entrypoint
- `cmd/broker-cli/`: HTTP and audit operations CLI
- `pkg/mcp/`: MCP tool handlers
- `pkg/api/`: broker HTTP/API surface
- `pkg/backends/slurm/`: Slurm adapter
- `pkg/backends/local/`: local direct-execution adapter
- `pkg/store/`: metadata persistence
- `pkg/cache/`: cache keying and lookup
- `pkg/policy/`: authorization and release policy
- `pkg/auth/`: caller identity extraction
- `pkg/authz/`: owner/admin authorization rules
- `pkg/audit/`: tamper-evident JSONL audit chain utilities
- `pkg/schemas/`: result schema registration and validation

Currently implemented broker features include:

- job submission, listing, status, result fetch, log fetch, and cancel over HTTP
- MCP tools for local job submission and retrieval
- file-backed and in-memory storage options for broker metadata
- Slurm job submission and status tracking
- local worker launch and PID/heartbeat tracking for workstation execution
- worker heartbeat ingestion for progress reporting
- release filtering for sensitive results and logs
- append-only audit logging with verification, rotation, and pruning support
