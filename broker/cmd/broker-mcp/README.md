# broker-mcp

Stdio-based MCP server for the local AI compute broker.

Current scope:

- `initialize`
- `ping`
- `tools/list`
- `tools/call`

Exposed tools:

- `submit_local_job`
- `submit_parallel_jobs`
- `get_job_status`
- `get_root_job_status`
- `fetch_result`
- `fetch_job_logs`
- `cancel_job`
- `list_local_capabilities`

This surface is intentionally thin and delegates all execution behavior to the existing broker service and Slurm backend layers.

Result and status contract:

- `get_job_status` returns the full broker job record, including:
  - `runtime_diagnostics`
  - `execution_quality`
  - `degraded_local_execution`
  - `retry_recommended`
- `fetch_result` returns the broker-filtered release view with the same summary fields.
- `execution_quality` is the preferred machine-readable branch point for orchestrators:
  - `real_local`
  - `degraded_local`
  - `no_real_backend`
- `runtime_diagnostics` is the detailed broker-ingested local-runtime summary. It excludes the raw runtime URL and can include:
  - `backend_name`
  - `backend_mode`
  - `selected_model`
  - `resource_tier`
  - `endpoint_configured`
  - `llm_available`
  - `timeout_seconds`
  - `last_error`

Session identity:

- interactive clients should set identity during `initialize`
- example:
  - `{"auth":{"actor":"alice","role":"user"}}`
- unattended runs may use:
  - `BROKER_MCP_ACTOR`
  - `BROKER_MCP_ROLE`
- if neither session auth nor env fallback is provided, tool execution is denied

Audit logging:

- `BROKER_AUDIT_LOG_PATH`
  - append-only JSONL audit sink shared with the broker control plane
  - records MCP-attributed submit, fetch, list, and cancel actions
  - records are hash-chained with `prev_hash` and `event_hash`
- `BROKER_AUDIT_VERIFY_MODE`
  - startup integrity policy for the active audit chain
  - values: `fail`, `warn`, `off`
