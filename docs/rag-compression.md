# RAG Compression

## Purpose

RAG compression is a first-class broker capability. Local workers should not simply summarize raw repositories, logs, documents, or incident artifacts. They should discover, index, retrieve, rerank, deduplicate, and compress the minimum useful evidence into structured evidence packs before anything is returned to a remote orchestrator.

The remote LLM remains the final synthesizer. It receives compact evidence packs, not raw local data, unless the user explicitly authorizes raw disclosure.

## Target Flow

```text
Remote LLM orchestrator
  |
  v
MCP Broker
  |
  v
Slurm / local / future backends
  |
  v
Local RAG compression workers
  |
  +--> input discovery
  +--> chunking
  +--> local indexing
  +--> retrieval
  +--> reranking
  +--> deduplication
  +--> evidence-preserving compression
  +--> JSON validation
  |
  v
Compressed evidence pack
  |
  v
Remote synthesis
```

## Pipeline

### 1. Input Discovery

Workers resolve only broker-authorized inputs from `input_manifest.json`.

Discovery should produce a manifest with:

- normalized paths or object IDs
- content hashes
- file sizes and language or MIME hints
- classifications
- commit SHA, tree hash, or log stream identity where available
- inclusion and exclusion reasons

Raw input content remains local.

### 2. Chunking

Chunking is task-aware.

Recommended strategies:

- code: tree-sitter-aware chunks by symbol, method, class, import region, and callsite neighborhood
- logs: timestamp-, phase-, exception-, and stack-trace-aware chunks
- documents: heading-, page-, paragraph-, and table-aware chunks
- git data: diff hunk-, commit-, and file-history-aware chunks

Every chunk must retain source references:

- file path or artifact URI
- line range, byte range, page range, timestamp range, or commit range
- content hash of the source input
- chunk hash

### 3. Local Indexing

Indexes are local-only by default.

Index types:

- ripgrep result indexes for exact text and regex search
- BM25 indexes for lexical retrieval
- tree-sitter symbol and callsite indexes for code
- embedding indexes for documents and semantically phrased queries
- stack-trace and path indexes for debugging
- git diff and history indexes for change-aware retrieval

Indexes may be persisted as artifacts and cache entries, but should not be returned to the remote model except as metadata.

### 4. Retrieval

Retrieval should combine deterministic and semantic signals.

Supported strategies:

- `ripgrep/BM25`: default for logs, code, exact errors, identifiers, and stack frames
- `tree_sitter`: default for symbol-aware code navigation and call graph expansion
- `embeddings`: default for prose documents, design docs, and semantic search
- `stack_trace_path`: default for test failures and runtime exceptions
- `git_diff_history`: default for regressions, recent changes, and patch review

The broker should expose retrieval strategy choice in task params, but workers may use hybrid retrieval when the task benefits from it.

### 5. Reranking

Reranking orders retrieved chunks by usefulness to the user question or debugging goal.

Signals may include:

- exact query term hits
- path, package, or symbol proximity
- stack-frame depth
- recency in git history
- test failure relevance
- embedding similarity
- local reranker model score
- deterministic heuristics such as error severity

Reranker outputs should be cached when the query, corpus hash, index version, and reranker version match.

### 6. Deduplication

Deduplication prevents the remote model from seeing repeated evidence.

Workers should collapse:

- repeated log lines
- repeated stack frames
- generated-file duplicates
- vendored dependency copies
- identical chunks by content hash
- near-duplicate snippets by normalized text or AST shape
- repeated evidence from retries or repeated test failures

Deduplication must keep provenance for all collapsed sources.

### 7. Evidence-Preserving Compression

Compression transforms retrieved chunks into compact evidence items. It must preserve references, not just meaning.

Each compressed evidence item should include:

- claim or observation
- evidence type
- source refs with path, line range, timestamp range, page range, commit hash, or artifact ID
- source hash or chunk hash
- relevance score
- confidence
- compression ratio or token accounting
- redaction status

Inline raw excerpts should be short, policy-screened, and optional. Restricted, PHI, secret-adjacent, and proprietary inputs should normally return references and derived observations only.

### 8. JSON Validation

Workers validate evidence packs before declaring success.

Validation should check:

- schema conformance
- all evidence refs resolve to local artifacts or indexed chunks
- token budgets are respected
- required provenance fields are present
- raw excerpt fields obey policy limits
- artifact and chunk hashes match

### 9. Remote Synthesis

The remote LLM receives only the validated evidence pack plus compact task metadata.

Remote synthesis should:

- reason over ranked evidence
- ask for follow-up local retrieval if evidence is insufficient
- propose fixes or next actions based on citations
- avoid fabricating source details not present in the evidence pack

## Evidence Pack Contract

An evidence pack is the northbound output of RAG compression.

Suggested shape:

```json
{
  "schema_name": "rag_evidence_pack_v1",
  "schema_version": "1.0.0",
  "payload": {
    "query": "Why did the build fail?",
    "input_scope": {
      "classification": "restricted",
      "source_refs": ["repo:sha256:...", "log:sha256:..."]
    },
    "retrieval": {
      "strategies": ["ripgrep", "stack_trace_path", "git_diff_history"],
      "chunks_considered": 4821,
      "chunks_retrieved": 120,
      "chunks_reranked": 40,
      "chunks_compressed": 12
    },
    "evidence": [
      {
        "id": "ev_001",
        "kind": "build_error",
        "claim": "The compiler cannot find a generated header after code generation was skipped.",
        "source_refs": [
          {
            "artifact_id": "artifact_build_log",
            "path": "build.log",
            "line_start": 4412,
            "line_end": 4431,
            "content_hash": "sha256:..."
          }
        ],
        "relevance": 0.96,
        "confidence": 0.91,
        "redaction": "no_raw_excerpt"
      }
    ],
    "budget": {
      "retrieved_chunk_tokens": 64000,
      "compressed_tokens": 2800,
      "final_pack_tokens": 3600,
      "remote_context_budget": 12000
    },
    "warnings": []
  }
}
```

## Token Budgets

The broker should enforce explicit budgets at every stage.

Required budget controls:

- `retrieved_chunk_budget`: maximum token-equivalent size of retrieved chunks before compression
- `per_chunk_compression_budget`: maximum output tokens per compressed evidence item
- `final_evidence_pack_budget`: maximum token-equivalent size returned to the remote model
- `remote_model_context_budget`: maximum expected remote-context use after tool response overhead

Budget enforcement order:

1. retrieval stops or spills into background when the retrieved chunk budget is hit
2. reranking trims to the best candidate chunks
3. compression emits bounded evidence items
4. final pack builder drops or groups lower-ranked evidence
5. JSON validator rejects over-budget packs

Budget decisions should be visible in `warnings` and progress metrics.

## Cacheable Assets

RAG compression should cache:

- file hashes
- directory and repository manifests
- chunk manifests
- tree-sitter parse outputs
- symbol indexes
- BM25 and ripgrep result indexes
- embeddings
- vector indexes
- retrieval results
- reranker outputs
- deduplicated evidence sets
- compressed evidence packs
- local model outputs

Cache keys must include:

- input content hashes
- query or task params
- retrieval strategy versions
- chunker version
- embedding model version
- reranker model or heuristic version
- compression model and template version
- schema version
- policy mode and namespace

## Scheduling And GPU Tiers

RAG compression should prefer low-contention compute.

Recommended default tiers:

- `p40-rag-compression`: default for chunk compression, reranking, small local LLM passes, and embedding jobs that fit 24 GB VRAM
- `cpu-rag-indexing`: default for discovery, hashing, chunking, ripgrep, BM25, tree-sitter parsing, and deduplication
- `a100-reasoning`: reserved for hard reasoning, large-context compression, high-quality reranking, or patch generation

P40 workers should be the first GPU choice for routine RAG compression because they can absorb token-heavy local work without tying up A100 capacity. A100 workers should be selected only when the broker estimates that the job needs larger context, larger models, patch synthesis, or higher-quality reasoning.

All tiers should run as normal scheduled jobs with low-priority or preemptible QoS unless the caller requests and is authorized for a higher priority.

Concrete broker config for a P40-first cluster:

```bash
BROKER_BACKEND=slurm
BROKER_SLURM_MODE=command
BROKER_SLURM_SCRIPT_PATH=deploy/slurm/broker_worker.slurm
BROKER_SLURM_PARTITION_CPU=cpu
BROKER_SLURM_PARTITION_P40=hpc
BROKER_SLURM_PARTITION_A100=gpu-a100
BROKER_SLURM_NODELIST_P40=pllimsksparky[1-4]
BROKER_MODEL_PROFILE_P40=gpt-oss-20b.p40
BROKER_MODEL_PROFILE_A100=qwen3-coder-30b.a100
```

With that mapping:

- `cpu-rag-indexing` lands on `cpu`
- `p40-rag-compression` lands on `hpc` and prefers `pllimsksparky[1-4]`
- `a100-reasoning` lands on `gpu-a100`

Default model routing can also be tier-specific:

- `p40-rag-compression` -> `gpt-oss-20b.p40`
- `a100-reasoning` -> `qwen3-coder-30b.a100`

The broker's retry recommendation flow already escalates by tier. Once these partition variables are set, retries can move from CPU to P40 and from P40 to A100 without any client-side partition logic.

## MCP Tools

The broker should expose RAG-aware tools in addition to the generic job tools.

Formal MCP registration schemas are defined in [MCP Tools And Broker API](./mcp-tools.md). The examples below show the intended request and response shape for each tool.

### `rag_compress`

Compress local inputs into an evidence pack for a query or investigation goal.

Input schema:

```json
{
  "query": "Why does the build fail?",
  "input_refs": [
    {
      "type": "repo",
      "uri": "file:///workspace/repo",
      "content_hash": "sha256:..."
    },
    {
      "type": "log",
      "uri": "file:///workspace/build.log",
      "content_hash": "sha256:..."
    }
  ],
  "task_params": {
    "retrieval_strategies": ["ripgrep", "bm25", "stack_trace_path", "git_diff_history"],
    "include_inline_excerpts": false,
    "evidence_kinds": ["build_error", "related_code", "recent_change"]
  },
  "constraints": {
    "retrieved_chunk_budget": 64000,
    "per_chunk_compression_budget": 384,
    "final_evidence_pack_budget": 4000,
    "remote_model_context_budget": 12000,
    "max_runtime_seconds": 900,
    "confidentiality": "local_only"
  },
  "execution_profile": {
    "backend": "slurm",
    "tier": "p40-rag-compression",
    "qos": "preemptible"
  },
  "idempotency_key": "optional-client-key"
}
```

Output schema:

```json
{
  "job_id": "job_01...",
  "state": "queued",
  "result_schema": "rag_evidence_pack_v1",
  "cache": {
    "status": "miss"
  },
  "status_url": "/v1/jobs/job_01...",
  "result_url": "/v1/jobs/job_01.../result"
}
```

### `debug_with_local_context`

Run a debugging-oriented RAG workflow over logs, stack traces, tests, repo paths, and recent git changes.

Input schema:

```json
{
  "problem": "Unit tests fail after the authz refactor.",
  "input_refs": [
    {"type": "repo", "uri": "file:///workspace/repo"},
    {"type": "log", "uri": "file:///workspace/test.log"}
  ],
  "task_params": {
    "stack_trace": "optional pasted stack trace or artifact ref",
    "failing_tests": ["TestGetJobForbiddenForDifferentActor"],
    "changed_since": "HEAD~5",
    "suspect_paths": ["broker/pkg/service", "broker/pkg/authz"],
    "retrieval_strategies": ["stack_trace_path", "ripgrep", "tree_sitter", "git_diff_history"]
  },
  "constraints": {
    "retrieved_chunk_budget": 96000,
    "final_evidence_pack_budget": 6000,
    "remote_model_context_budget": 16000
  },
  "execution_profile": {
    "tier": "p40-rag-compression"
  }
}
```

Output schema:

```json
{
  "job_id": "job_01...",
  "state": "queued",
  "result_schema": "debug_evidence_pack_v1",
  "status_url": "/v1/jobs/job_01..."
}
```

### `summarize_logs`

Run log-specific retrieval, clustering, deduplication, and evidence compression.

Input schema:

```json
{
  "input_refs": [
    {"type": "log", "uri": "file:///workspace/build.log", "content_hash": "sha256:..."}
  ],
  "task_params": {
    "query": "Find the root failure and related warnings.",
    "time_window": {
      "start": "2026-07-01T13:00:00Z",
      "end": "2026-07-01T13:30:00Z"
    },
    "deduplicate_repeated_lines": true,
    "cluster_stack_traces": true
  },
  "constraints": {
    "retrieved_chunk_budget": 128000,
    "per_chunk_compression_budget": 256,
    "final_evidence_pack_budget": 4000
  },
  "execution_profile": {
    "tier": "p40-rag-compression"
  }
}
```

Output schema:

```json
{
  "job_id": "job_01...",
  "state": "queued",
  "result_schema": "log_evidence_pack_v1",
  "status_url": "/v1/jobs/job_01..."
}
```

### `inspect_repo`

Run repository discovery, chunking, indexing, and evidence compression for code understanding.

Input schema:

```json
{
  "input_refs": [
    {"type": "repo", "uri": "file:///workspace/repo", "content_hash": "sha256:..."}
  ],
  "task_params": {
    "query": "Explain the MCP tool dispatch and auth boundaries.",
    "paths": ["broker/pkg/mcp", "broker/pkg/service", "broker/pkg/auth"],
    "languages": ["go"],
    "retrieval_strategies": ["tree_sitter", "ripgrep", "bm25"],
    "include_dependency_edges": true
  },
  "constraints": {
    "retrieved_chunk_budget": 96000,
    "final_evidence_pack_budget": 5000
  },
  "execution_profile": {
    "tier": "p40-rag-compression"
  }
}
```

Output schema:

```json
{
  "job_id": "job_01...",
  "state": "queued",
  "result_schema": "repo_inspection_pack_v1",
  "status_url": "/v1/jobs/job_01..."
}
```

### `propose_patch`

Use local evidence packs and, when needed, higher-tier local reasoning to generate a candidate patch package.

Input schema:

```json
{
  "problem": "Fix the authorization regression identified by evidence pack evpack_01.",
  "input_refs": [
    {"type": "repo", "uri": "file:///workspace/repo"},
    {"type": "artifact", "uri": "artifact://evpack_01"}
  ],
  "task_params": {
    "patch_style": "minimal",
    "allowed_paths": ["broker/pkg/service", "broker/pkg/api"],
    "validation_commands": ["go test ./broker/pkg/service ./broker/pkg/api"]
  },
  "constraints": {
    "retrieved_chunk_budget": 128000,
    "final_evidence_pack_budget": 8000,
    "max_runtime_seconds": 1800
  },
  "execution_profile": {
    "tier": "a100-reasoning"
  }
}
```

Output schema:

```json
{
  "job_id": "job_01...",
  "state": "queued",
  "result_schema": "patch_proposal_pack_v1",
  "status_url": "/v1/jobs/job_01..."
}
```

These tools should still map to broker jobs internally so status, cancellation, caching, auditing, and policy enforcement remain uniform.

Existing lifecycle tools remain unchanged:

- `get_job_status({"job_id": "job_01..."})`
- `fetch_result({"job_id": "job_01...", "include_artifacts": false})`
- `cancel_job({"job_id": "job_01..."})`

## Broker API Endpoints

RAG-specific MCP tools should map to ordinary broker jobs. The HTTP API should support both the generic job endpoint and explicit convenience endpoints.

Generic endpoint:

- `POST /v1/jobs` with `task_type` set to `rag_compress`, `debug_with_local_context`, `summarize_logs`, `inspect_repo`, or `propose_patch`

Convenience endpoints:

- `POST /v1/rag/compressions`
- `POST /v1/rag/debug-sessions`
- `POST /v1/logs:summarize`
- `POST /v1/repos:inspect`
- `POST /v1/patches:propose`

Lifecycle endpoints:

- `GET /v1/jobs/{job_id}`
- `GET /v1/jobs/{job_id}/result`
- `GET /v1/jobs/{job_id}/logs`
- `POST /v1/jobs/{job_id}:cancel`
- `GET /v1/roots/{root_job_id}`

RAG artifact endpoints:

- `GET /v1/rag/evidence-packs/{artifact_id}/metadata`
- `GET /v1/rag/indexes/{artifact_id}/metadata`
- `POST /v1/rag/cache:lookup`

Raw chunk text, vector payloads, and local index contents should not have default HTTP export endpoints. If future deployments need raw export, it should be behind explicit policy approval and audited override endpoints.

## Slurm Job Types

Slurm remains a backend implementation detail, but the broker should classify RAG work into predictable job types for scheduling and observability.

| Job type | Default tier | Typical resources | Purpose |
| --- | --- | --- | --- |
| `rag_discovery` | `cpu-rag-indexing` | CPU, memory, no GPU | input manifests, hashing, classification, inclusion/exclusion |
| `rag_chunk_index` | `cpu-rag-indexing` | CPU, memory, no GPU | chunk manifests, tree-sitter parses, BM25 indexes, rg indexes |
| `rag_embed` | `p40-rag-compression` | 1x P40 when needed | embedding generation and vector index updates |
| `rag_retrieve_rerank` | `cpu-rag-indexing` or `p40-rag-compression` | CPU or 1x P40 | hybrid retrieval and local reranking |
| `rag_compress` | `p40-rag-compression` | 1x P40 | bounded evidence-preserving compression |
| `rag_validate_pack` | `cpu-rag-indexing` | CPU, no GPU | JSON schema, hash, source-ref, and token-budget validation |
| `rag_patch_generate` | `a100-reasoning` | 1x A100 or configured large GPU profile | patch proposal, large-context reasoning, difficult synthesis |

Slurm submission guidance:

- use ordinary `sbatch` jobs or arrays
- use low-priority or preemptible QoS by default
- prefer arrays for homogeneous chunk compression or embedding shards
- encode job names as `broker-{task_type}-{stage}`, for example `broker-rag_compress-compress`
- include stage metadata in the worker `execution_plan.json`
- never reserve GPUs permanently for broker workers

P40 selection rules:

- default GPU tier for `rag_embed`, `rag_retrieve_rerank`, and `rag_compress`
- expected model profile should fit in 24 GB VRAM with practical context limits
- spill to CPU or A100 only when the planner predicts P40 cannot meet context, latency, or quality requirements

A100 selection rules:

- use for `rag_patch_generate`
- use for very large-context compression
- use for hard reasoning or high-accuracy local verifier/reranker passes
- require explicit planner reason in execution metadata

## Data Model

The RAG pipeline extends the base broker model with first-class intermediate entities.

### `InputManifest`

```json
{
  "manifest_id": "manifest_01...",
  "input_refs": ["input_01..."],
  "corpus_hash": "sha256:...",
  "files": [
    {
      "path": "broker/pkg/service/service.go",
      "content_hash": "sha256:...",
      "size_bytes": 12345,
      "language": "go",
      "classification": "restricted",
      "commit": "abc123"
    }
  ]
}
```

### `Chunk`

```json
{
  "chunk_id": "chunk_01...",
  "manifest_id": "manifest_01...",
  "chunk_hash": "sha256:...",
  "source_ref": {
    "path": "broker/pkg/service/service.go",
    "line_start": 120,
    "line_end": 188,
    "commit": "abc123",
    "content_hash": "sha256:..."
  },
  "chunk_type": "function",
  "token_estimate": 742,
  "classification": "restricted"
}
```

### `LocalIndex`

```json
{
  "index_id": "index_01...",
  "manifest_id": "manifest_01...",
  "index_type": "tree_sitter_symbol",
  "strategy_version": "tree-sitter-go@1.0.0",
  "artifact_id": "artifact_symbol_index",
  "chunk_count": 1833,
  "classification": "restricted"
}
```

### `RetrievalRun`

```json
{
  "retrieval_id": "retrieval_01...",
  "query_hash": "sha256:...",
  "manifest_id": "manifest_01...",
  "strategies": ["ripgrep", "tree_sitter", "git_diff_history"],
  "retrieved_chunk_ids": ["chunk_01", "chunk_02"],
  "reranked_chunk_ids": ["chunk_02", "chunk_01"],
  "budget": {
    "retrieved_chunk_tokens": 64000
  }
}
```

### `EvidencePack`

```json
{
  "evidence_pack_id": "evpack_01...",
  "job_id": "job_01...",
  "schema_name": "rag_evidence_pack_v1",
  "artifact_id": "artifact_evpack_01",
  "query_hash": "sha256:...",
  "source_manifest_ids": ["manifest_01..."],
  "evidence_count": 12,
  "final_pack_tokens": 3600,
  "policy_mode": "restricted_evidence_only",
  "classification": "restricted"
}
```

These entities can be implemented as separate tables later or as typed artifacts plus metadata in the first implementation.

## Cache Keys

RAG cache keys should be explicit by layer.

### File Hash Cache

```text
file_hash:v1:{namespace}:{path}:{mtime}:{size}
```

Value includes content hash. The broker must verify stale mtime/size entries before reuse when correctness matters.

### Chunk Manifest Cache

```text
chunks:v1:{namespace}:{corpus_hash}:{chunker}:{include_rules_hash}
```

### Index Cache

```text
index:v1:{namespace}:{corpus_hash}:{index_type}:{strategy_version}:{chunk_manifest_hash}
```

### Embedding Cache

```text
embedding:v1:{namespace}:{chunk_hash}:{embedding_model}:{embedding_dimension}:{normalization}
```

### Retrieval Cache

```text
retrieval:v1:{namespace}:{corpus_hash}:{query_hash}:{strategy_set_hash}:{retrieval_budget}:{index_versions_hash}
```

### Rerank Cache

```text
rerank:v1:{namespace}:{retrieval_hash}:{reranker}:{reranker_version}:{top_k}
```

### Evidence Pack Cache

```text
evidence_pack:v1:{namespace}:{rerank_hash}:{compressor}:{template_version}:{policy_mode}:{final_budget}:{schema}
```

### Local Model Output Cache

```text
model_output:v1:{namespace}:{model}:{model_version}:{prompt_template}:{input_hash}:{output_schema}:{policy_mode}
```

All restricted, PHI, secret-adjacent, and proprietary caches must be namespace-scoped. Cache hits must still pass pre-release policy.

## Policy Rules

Default posture:

- raw repositories, logs, documents, PHI, and proprietary data remain local
- remote LLM receives evidence packs only
- chunk text, embeddings, and local indexes remain local-only unless explicitly approved
- inline excerpts are opt-in, bounded, redacted, and policy-screened
- source refs are allowed when policy permits path, timestamp, line, and commit metadata release

For high-sensitivity inputs, evidence items should use artifact IDs and derived claims rather than inline text.

## Failure Handling

Workers should fail closed when evidence preservation cannot be guaranteed.

Failure examples:

- source refs cannot be resolved
- chunk hashes do not match source manifests
- evidence pack exceeds final budget
- JSON validation fails
- redaction detects disallowed raw content
- retrieval coverage is too low for a confident answer

Low-coverage cases may return a valid evidence pack with `warnings` and `confidence` signals, allowing the remote orchestrator to request a narrower follow-up retrieval.

### Error Codes

RAG compression should use machine-readable error codes.

| Code | Retryable | Meaning |
| --- | --- | --- |
| `INPUT_DISCOVERY_FAILED` | false | an input ref could not be resolved or was outside policy scope |
| `INPUT_HASH_MISMATCH` | false | discovered content did not match the declared hash |
| `CHUNKING_FAILED` | true | chunker or parser failed for a subset of inputs |
| `INDEX_BUILD_FAILED` | true | lexical, symbol, or vector index creation failed |
| `RETRIEVAL_BUDGET_EXCEEDED` | false | retrieval exceeded configured chunk budget before enough evidence was selected |
| `RERANK_FAILED` | true | local reranker failed or returned malformed scores |
| `COMPRESSION_FAILED` | true | local model or compressor failed |
| `EVIDENCE_REF_INVALID` | false | compressed evidence referenced a missing path, line range, timestamp, commit, chunk, or artifact |
| `EVIDENCE_PACK_OVER_BUDGET` | false | final evidence pack exceeded the configured return budget |
| `RAG_SCHEMA_INVALID` | false | worker output failed JSON schema validation |
| `POLICY_REDACTION_REQUIRED` | false | output contains raw content that must be removed or converted to local-only artifacts |
| `REMOTE_CONTEXT_BUDGET_EXCEEDED` | false | pack is valid locally but too large for the declared remote context budget |
| `GPU_TIER_UNAVAILABLE` | true | selected P40 or A100 tier is unavailable within timeout |

Error response shape:

```json
{
  "error": {
    "code": "EVIDENCE_REF_INVALID",
    "message": "Evidence item ev_003 references a line range outside the indexed chunk.",
    "retryable": false,
    "stage": "json_validation",
    "details": {
      "job_id": "job_01...",
      "evidence_id": "ev_003",
      "chunk_id": "chunk_abc"
    }
  }
}
```

### Degraded Success

Some partial outcomes should be represented as successful evidence packs with warnings, not failed jobs.

Examples:

- optional embedding index unavailable but BM25 and tree-sitter retrieval succeeded
- some binary or generated files skipped by policy
- retrieval hit budget and lower-ranked evidence was trimmed
- inline excerpts withheld while evidence references remain valid

Warnings should be structured:

```json
{
  "code": "INLINE_EXCERPTS_WITHHELD",
  "message": "Restricted inputs were compressed into claims and source refs without raw excerpts.",
  "stage": "pre_release_policy"
}
```

## UX Flows

### Interactive Debugging

1. User asks the remote agent to diagnose a failure.
2. Remote agent calls `debug_with_local_context` with repo and log refs.
3. Broker runs discovery, indexing, retrieval, compression, and validation locally.
4. Agent polls `get_job_status`.
5. Agent calls `fetch_result`.
6. Agent receives `debug_evidence_pack_v1` and synthesizes a concise diagnosis with citations.
7. If evidence is insufficient, agent calls `rag_compress` again with narrower paths, tests, or stack frames.

Remote-visible data:

- compressed claims
- source refs
- confidence and warnings
- optional redacted excerpts if policy allows

Local-only data:

- raw source
- raw logs
- chunk text
- embeddings
- vector indexes
- full artifacts unless explicitly approved

### Log Summarization

1. Agent calls `summarize_logs` with one or more log refs.
2. Worker clusters repeated lines, stack traces, phases, and timestamps.
3. Worker retrieves the root error neighborhood and causal warnings.
4. Worker emits a `log_evidence_pack_v1` result with timeline and evidence refs.
5. Remote agent explains the failure from the evidence pack.

### Repository Inspection

1. Agent calls `inspect_repo` with a repo ref and query.
2. Worker builds or reuses file hashes, chunk manifests, symbol indexes, and lexical indexes.
3. Worker retrieves relevant files, symbols, callsites, and docs.
4. Worker compresses evidence into subsystem, entrypoint, dependency, and risk observations.
5. Remote agent uses the pack to answer without reading the raw repo.

### Patch Proposal

1. Agent first obtains an evidence pack through `debug_with_local_context` or `rag_compress`.
2. Agent calls `propose_patch` with the evidence pack artifact and allowed paths.
3. Broker selects `a100-reasoning` only if the patch requires hard reasoning or large-context synthesis.
4. Worker generates patch artifacts, rationale, risks, and validation steps.
5. Broker returns patch metadata and may withhold full diffs by policy.
6. Remote agent presents candidate changes and validation commands.

### Long-Running Background Investigation

1. Agent calls `rag_compress` or `inspect_repo` with broad scope and background priority.
2. Broker decomposes discovery, indexing, embedding, retrieval, and compression into normal jobs or arrays.
3. Agent polls root status through `get_root_job_status`.
4. Broker caches intermediates aggressively.
5. Agent fetches final evidence pack when ready.
6. Follow-up questions reuse cached chunks, indexes, retrieval results, and compressed packs.

## Remote Synthesis Contract

The remote model should treat evidence packs as the only supported basis for final answers.

Remote synthesis rules:

- cite evidence IDs, paths, line ranges, timestamps, or commit refs supplied by the evidence pack
- do not invent file names, line numbers, commits, or log timestamps
- ask for follow-up local retrieval when the evidence pack is insufficient
- do not request raw export unless the user explicitly asks and policy allows it
- keep final answers within the declared remote context budget
