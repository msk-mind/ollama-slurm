# Broker Quickstart

This quickstart uses the implemented broker binaries in this repository.

It is intentionally scoped to local validation:

- no real Slurm cluster required
- no permanent GPU reservation
- no external services required

The fastest way to validate the current control plane is to use the fake-Slurm end-to-end smoke test first, then optionally run the broker server and MCP server directly.

## Option 1: End-To-End Smoke Test

This is the shortest path to confirm that the broker server, Slurm adapter, worker runtime, result ingestion, and broker CLI all work together.

Run:

```bash
tests/e2e/smoke_command_mode.sh
```

What it does:

- creates fake `sbatch`, `sacct`, `scancel`, and `squeue` commands
- starts `broker-server` in command-mode Slurm emulation
- submits a `document_summary` job
- waits for completion
- fetches the structured result from the broker

If this succeeds, the local broker control plane is functioning.

## Option 2: Run The Broker Server Locally

This mode is useful when you want to inspect the HTTP API directly.

Recommended local environment for direct execution on this machine or a MacBook:

```bash
export BROKER_LISTEN_ADDR="127.0.0.1:8081"
export BROKER_JOB_STORE_PATH=".broker/jobs.json"
export BROKER_RUN_ROOT_PATH=".broker/runs"
export BROKER_REPO_ROOT_PATH="$PWD"
export BROKER_BACKEND="local"
export BROKER_LOCAL_MODE="command"
export BROKER_LOCAL_SCRIPT_PATH="$PWD/deploy/local/broker_worker.sh"
export BROKER_AUDIT_LOG_PATH=".broker/audit.jsonl"
export BROKER_AUDIT_VERIFY_MODE="warn"
```

Start the server:

```bash
env -u GOROOT \
  GOCACHE=/tmp/ollama-slurm-gocache \
  GOPATH=/tmp/ollama-slurm-gopath \
  /usr/bin/go run ./broker/cmd/broker-server
```

Check health:

```bash
curl -sf http://127.0.0.1:8081/healthz
```

If you want to validate the Slurm adapter without a real cluster, use `tests/e2e/smoke_command_mode.sh`. If you want workstation execution, keep `BROKER_BACKEND=local`.

For opt-in Codex setup without changing your default global MCP configuration:

```bash
examples/mcp-clients/install_codex_profiles.sh
codex -p slurm-broker
codex -p local-broker
```

The generated profiles keep broker MCP wiring session-scoped, so a normal `codex` launch stays unchanged.

## Option 2B: Run The Broker Against A Real Slurm Cluster

If your cluster has lightly used P40 nodes and more constrained A100 nodes, start from:

```bash
cp configs/broker/slurm-p40-a100.env.example /tmp/ollama-slurm-broker.env
```

Then edit the partition names if your site uses different labels:

- `BROKER_SLURM_PARTITION_CPU`
- `BROKER_SLURM_PARTITION_P40`
- `BROKER_SLURM_PARTITION_A100`

If you want the P40 tier to prefer a specific node pool directly, also set:

- `BROKER_SLURM_NODELIST_P40=pllimsksparky[1-4]`
- optionally `BROKER_SLURM_CONSTRAINT_P40=p40`

For automatic tier-local model selection, set:

- `BROKER_MODEL_PROFILE_P40=gpt-oss-20b.p40`
- `BROKER_MODEL_PROFILE_A100=qwen3-coder-30b.a100`

For live local `llama.cpp` reranking/compression instead of deterministic fallback, also set:

- `BROKER_RUNTIME_LLAMACPP_BASE_URL=http://127.0.0.1:8080`
- optionally `BROKER_RUNTIME_LLAMACPP_TIMEOUT_SECONDS=20`

Load it and start the server:

```bash
set -a
source /tmp/ollama-slurm-broker.env
set +a

env -u GOROOT \
  GOCACHE=/tmp/ollama-slurm-gocache \
  GOPATH=/tmp/ollama-slurm-gopath \
  /usr/bin/go run ./broker/cmd/broker-server
```

With that profile:

- ordinary indexing work stays on CPU
- routine RAG compression defaults to the P40 tier and can prefer `pllimsksparky[1-4]`
- retries or harder patch-generation flows can escalate to the A100 tier
- workers receive an `execution_plan.json` with the broker-selected model profile, runtime, and runtime connection metadata

The broker does not reserve GPUs. Each request still runs as a normal Slurm job and releases resources on completion or preemption.

## Option 2C: Smoke A Live `llama.cpp` RAG Worker

If you already have a reachable `llama.cpp` server, you can validate that the RAG worker uses the staged runtime endpoint rather than deterministic fallback.

1. Start or forward a local `llama.cpp` endpoint.

Example with a forwarded local endpoint:

```bash
export BROKER_RUNTIME_LLAMACPP_BASE_URL="http://127.0.0.1:8080"
```

2. Start the broker with your Slurm profile loaded.

3. Submit a small RAG task:

```bash
curl -sf \
  -H 'Content-Type: application/json' \
  -X POST http://127.0.0.1:8081/v1/rag/compressions \
  -d '{
    "query": "What does this repository do?",
    "input_refs": [{"type":"repo","uri":"file://'"$PWD"'"}],
    "constraints": {
      "retrieved_chunk_budget": 16000,
      "per_chunk_compression_budget": 192,
      "final_evidence_pack_budget": 1200
    },
    "execution_profile": {
      "backend": "slurm",
      "tier": "p40-rag-compression"
    }
  }'
```

4. Fetch the job result and inspect:

- `payload.provenance`
- `payload.retrieval.runtime_backend_mode`
- `artifact_runtime_context`
- top-level `runtime_diagnostics`
- top-level `execution_quality`
- top-level `degraded_local_execution`
- top-level `retry_recommended`

If the runtime endpoint is reachable, those fields should show the live `llama.cpp` path instead of the deterministic fallback path.

## Option 2D: Smoke An Unreachable Local Runtime

If you want to validate the broker's degraded-path reporting, point the local runtime at an unreachable loopback port and submit the same kind of RAG task.

Example:

```bash
export BROKER_RUNTIME_LLAMACPP_BASE_URL="http://127.0.0.1:9"
export BROKER_RUNTIME_LLAMACPP_TIMEOUT_SECONDS="1"
```

Then submit a small `rag_compress` request and inspect `GET /v1/jobs/{job_id}` or `GET /v1/jobs/{job_id}/result`.

Expected broker-facing shape:

```json
{
  "runtime_diagnostics": {
    "backend_name": "llama.cpp",
    "backend_mode": "unavailable",
    "selected_model": "gpt-oss-20b.p40",
    "resource_tier": "p40-rag-compression",
    "endpoint_configured": true,
    "llm_available": false,
    "timeout_seconds": 1,
    "last_error": "<urlopen error [Errno 111] Connection refused>"
  },
  "execution_quality": "degraded_local",
  "degraded_local_execution": true,
  "retry_recommended": false
}
```

If the broker cannot obtain any real local retrieval backend at all, the result/status shape will move to:

```json
{
  "execution_quality": "no_real_backend",
  "degraded_local_execution": true,
  "retry_recommended": true
}
```

Current broker result/status summary fields:

- `runtime_diagnostics`
  - broker-ingested structured runtime summary from worker outputs
  - excludes the raw runtime URL
- `execution_quality`
  - `real_local`: a real local runtime served the job
  - `degraded_local`: local execution succeeded but relied on degraded heuristics or fallback paths
  - `no_real_backend`: the broker recommends retrying on a stronger or real local backend
- `degraded_local_execution`
  - explicit boolean for client filters and dashboards
- `retry_recommended`
  - explicit boolean derived from the broker retry recommendation path

These fields are present on both:

- `GET /v1/jobs/{job_id}`
- `GET /v1/jobs/{job_id}/result`

## Submit A Job With `broker-cli`

Create a small input file:

```bash
cat > /tmp/broker-demo.txt <<'EOF'
This is a small demo document.
It exists to validate broker job submission and structured summarization.
EOF
```

Submit:

```bash
env -u GOROOT \
  GOCACHE=/tmp/ollama-slurm-gocache \
  GOPATH=/tmp/ollama-slurm-gopath \
  BROKER_BASE_URL=http://127.0.0.1:8081 \
  /usr/bin/go run ./broker/cmd/broker-cli submit \
    --task-type document_summary \
    --input-uri file:///tmp/broker-demo.txt \
    --schema document_summary_v1
```

Watch:

```bash
env -u GOROOT \
  GOCACHE=/tmp/ollama-slurm-gocache \
  GOPATH=/tmp/ollama-slurm-gopath \
  BROKER_BASE_URL=http://127.0.0.1:8081 \
  /usr/bin/go run ./broker/cmd/broker-cli watch <job-id>
```

Fetch the result:

```bash
env -u GOROOT \
  GOCACHE=/tmp/ollama-slurm-gocache \
  GOPATH=/tmp/ollama-slurm-gopath \
  BROKER_BASE_URL=http://127.0.0.1:8081 \
  /usr/bin/go run ./broker/cmd/broker-cli result <job-id>
```

## Option 3: Run The MCP Server

The broker also exposes a stdio MCP server for MCP-capable agents.

Use the helper script:

```bash
examples/mcp-clients/run_broker_mcp.sh
```

That script sets the common local broker environment and launches:

```bash
/usr/bin/go run ./broker/cmd/broker-mcp
```

The implemented MCP tools are:

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

For generic MCP wiring examples, see:

- [examples/mcp-clients/mcp-integration.md](../examples/mcp-clients/mcp-integration.md)
- [examples/mcp-clients/generic-stdio-config.json](../examples/mcp-clients/generic-stdio-config.json)

## Current Task Types

The current worker template and schema validation flow support:

- `document_summary`
- `log_analysis`
- `repo_summary`
- `rag_compress`
- `debug_with_local_context`
- `summarize_logs`
- `inspect_repo`
- `propose_patch`

Codex CLI has been validated against the current stdio MCP server.
GitHub Copilot CLI should still be treated as experimental until its MCP integration is verified end to end in the target environment.

## Notes

- `BROKER_AUDIT_VERIFY_MODE=warn` is convenient for local development because it allows startup even if the current audit file is absent or invalid.
- the default auth mode is `header`; for local testing, direct `curl` and `broker-cli` calls are simplest when auth policy is relaxed or fronted by a trusted environment
- the smoke test under `tests/e2e/` is the most reliable local validation path because it exercises the broker with a fake scheduler instead of assuming a real Slurm environment
