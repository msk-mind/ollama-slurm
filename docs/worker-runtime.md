# Worker Runtime

## Purpose

The worker runtime is the execution-side contract between the broker and local compute infrastructure. It is responsible for turning an immutable job specification into structured outputs and artifacts.

This document defines:

- worker responsibilities
- worker input contract
- runtime lifecycle
- heartbeat and progress reporting
- artifact and result emission
- isolation expectations

## Design Goals

- `deterministic contract`: workers receive explicit inputs and emit explicit outputs
- `portable`: the same worker contract should run on Slurm, Kubernetes, Ray, or standalone GPU servers
- `observable`: broker should be able to track liveness, progress, and terminal outcome
- `schema-first`: workers should emit structured results, not ad hoc prose blobs
- `least privilege`: workers should access only the staged data needed for the task
- `evidence-preserving compression`: workers should preserve source refs while reducing raw local inputs into compact evidence packs

## Non-Goals

The worker runtime should not:

- decide whether a task is authorized
- choose whether results may be exported remotely
- define MCP tool behavior
- directly expose raw results to the remote orchestrator
- own cache policy beyond recording reusable outputs and intermediates

## Worker Responsibilities

Each worker should:

- load an immutable job spec
- resolve staged inputs
- execute task-specific logic
- emit progress updates
- persist artifacts and structured results
- record metrics and terminal status

Each worker should not:

- scan arbitrary directories outside its allowed input scope
- infer hidden inputs from the environment
- assume a specific scheduler implementation

## Worker Input Contract

The broker should pass the worker a small, explicit execution bundle.

Suggested contents:

- `job_spec.json`
- `execution_plan.json`
- `input_manifest.json`
- credentials or scoped access tokens for artifact upload if needed
- output destination metadata

### `job_spec.json`

Contains:

- broker job ID
- task type
- task params
- requested output schema
- constraints
- policy context relevant to local execution

### `execution_plan.json`

Contains:

- selected model profile
- runtime backend
- resource expectations
- timeout
- container or runtime metadata

Current implementation notes:

- the broker stages `execution_plan.json` beside `job_spec.json` and `input_manifest.json`
- RAG workers should read it as the source of truth for selected model profile and runtime backend
- structured results and validation artifacts should carry that provenance so cache reuse and later analysis remain explainable
- worker pipelines should branch through a small runtime-adapter layer so `deterministic`, `llama.cpp`, `vLLM`, and `SGLang` can diverge without forking the whole task implementation
- the current `llama.cpp` adapter can use `RAG_LLAMA_CPP_BASE_URL` or `LLAMA_CPP_BASE_URL` for live local reranking and compression, with deterministic fallback when no endpoint is configured
- broker-managed runtime connectivity should be staged in `execution_plan.json`, for example runtime `base_url` and request timeout, so workers do not depend on ambient environment discovery

### `input_manifest.json`

Contains:

- resolved input refs
- content hashes
- classifications
- mount paths or fetch URIs
- optional chunk manifests or staged intermediate refs

Workers should trust these manifests, not rediscover their own input universe.

## Runtime Lifecycle

Recommended lifecycle:

1. Start process or container.
2. Load execution bundle.
3. Validate required files and environment.
4. Emit `starting` heartbeat.
5. Resolve inputs.
6. Discover authorized input scope and content hashes.
7. Chunk inputs with source references.
8. Build or reuse local indexes.
9. Retrieve, rerank, and deduplicate candidate chunks.
10. Compress evidence while preserving paths, line ranges, timestamps, commits, artifact IDs, and hashes.
11. Validate result against requested schema, policy, and token budgets.
12. Persist result bundle and artifact manifest.
13. Emit terminal heartbeat.
14. Exit cleanly.

## Progress And Heartbeats

Workers should report liveness and useful progress, not just terminal state.

Recommended heartbeat fields:

```json
{
  "job_id": "job_01...",
  "state": "running",
  "phase": "analyzing_chunks",
  "percent": 42,
  "timestamp": "2026-06-25T13:05:04Z",
  "metrics": {
    "chunks_processed": 84,
    "chunks_total": 200
  }
}
```

Recommended phases:

- `starting`
- `resolving_inputs`
- `discovering_inputs`
- `chunking`
- `indexing`
- `retrieving`
- `reranking`
- `deduplicating`
- `compressing_evidence`
- `preprocessing`
- `running_model`
- `postprocessing`
- `writing_artifacts`
- `validating_result`
- `completed`

Heartbeat transport options:

- broker callback API
- append-only heartbeat file in shared storage
- message queue or event sink

The first implementation can use simple broker polling of a heartbeat file or callback endpoint.

Current implementation notes:

- workers write `heartbeat.json` in the staged run directory
- the broker reads that file during job status refresh and exposes it as `job.progress`
- the Slurm batch wrapper writes bootstrap and terminal heartbeats even if a task-specific worker does not
- direct standalone worker execution should also emit `heartbeat.json` under `--output-dir`

## Task Execution Patterns

Workers may run several execution styles under the same contract.

### Tool-First Workers

Use deterministic local tools before model inference.

Examples:

- `rg`
- tree-sitter
- Semgrep
- test log parsers
- build graph or symbol extractors

Best for:

- code search
- static analysis
- preprocessing

### Model-First Workers

Use local LLMs or embedding models as the main compute step.

Examples:

- summarization
- root cause synthesis
- patch generation
- embeddings

### Hybrid Workers

Combine tool-first extraction with model-based synthesis.

Examples:

- test failure analysis
- repo summary
- root cause analysis

The hybrid pattern should be the default for many high-value tasks because it reduces local model cost and improves structure. For RAG compression tasks, workers should use deterministic tools for discovery, chunking, indexing, retrieval, and deduplication before using local models for reranking or evidence compression.

### RAG Compression Workers

Perform evidence-preserving compression before any remote synthesis.

Pipeline:

1. input discovery
2. chunking
3. local indexing
4. retrieval
5. reranking
6. deduplication
7. evidence-preserving compression
8. JSON validation

Expected worker outputs:

- `rag_evidence_pack_v1` or task-specific evidence-pack schema
- input manifest artifact
- chunk manifest artifact
- local index metadata artifacts
- retrieval and rerank metadata
- validation report

Raw chunk text, embeddings, and local indexes should remain local-only by default.

## Output Contract

Workers should emit three main output classes.

### 1. Structured Result

Required terminal payload matching the requested schema.

Suggested path:

- `result.json`

### 2. Artifact Manifest

Index of artifacts produced during execution.

Suggested path:

- `artifacts.json`

Suggested shape:

```json
[
  {
    "artifact_id": "artifact_01...",
    "artifact_type": "redacted_excerpt",
    "content_hash": "sha256:abc123...",
    "path": "/outputs/artifacts/log_excerpt_1.txt",
    "classification": "restricted"
  }
]
```

### 3. Execution Metadata

Useful for debugging, provenance, and cacheability.

Suggested path:

- `run-metadata.json`

Suggested contents:

- worker version
- runtime backend version
- tool versions
- model name and revision
- timings
- resource usage if available

## Result Validation

Workers should validate output before declaring success.

Validation steps:

- required files exist
- `result.json` conforms to requested schema
- all artifact references in result exist in artifact manifest
- content hashes match emitted artifacts

If validation fails:

- worker should emit failure metadata
- broker should treat the run as failed, not partially successful

## Artifact Strategy

Artifacts should be explicit and typed.

Common artifact categories:

- `excerpt`
- `redacted_excerpt`
- `patch_diff`
- `chunk_manifest`
- `input_manifest`
- `symbol_index`
- `embedding_index`
- `retrieval_result`
- `rerank_result`
- `evidence_pack`
- `validation_report`
- `execution_log`
- `tool_output`

Workers should prefer artifact references over stuffing large data into the result payload.

## Local Model Runtime Integration

Workers may call:

- `llama.cpp`
- `vLLM`
- `SGLang`
- embedding model servers
- direct library inference if appropriate

The worker contract should not depend on one runtime. The worker only needs:

- configured model identity
- invocation settings
- expected structured output mode if supported

## Error Handling

Workers should distinguish between:

- invalid input
- tool failure
- model failure
- timeout
- OOM
- artifact write failure
- result validation failure

Suggested terminal error metadata:

```json
{
  "job_id": "job_01...",
  "status": "failed",
  "error": {
    "code": "RESULT_SCHEMA_INVALID",
    "message": "Worker produced a result that does not match root_cause_analysis_v1.",
    "retryable": false
  }
}
```

## Isolation Expectations

Workers should execute in constrained environments.

Recommended defaults:

- containerized where possible
- explicit input mounts only
- minimal environment inheritance
- network egress disabled by default
- non-root execution where feasible
- bounded CPU, memory, GPU, and wall-clock limits

These are important because workers may process sensitive repos, logs, and documents.

## Versioning And Reproducibility

Every worker run should record:

- worker implementation version
- schema version
- model logical name and version
- prompt/template version if used
- tool versions
- runtime backend version

This metadata is required for:

- debugging
- reproducibility
- cache correctness

## Testing Strategy

Each worker type should have:

- fixture-based unit tests for parsing and postprocessing
- schema validation tests
- integration tests with staged input manifests
- failure-path tests for timeouts and malformed outputs

The broker should also run cross-worker conformance tests to ensure all workers obey the same lifecycle and output contract.
