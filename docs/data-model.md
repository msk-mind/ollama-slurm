# Data Model

## Goals

The broker data model should support:

- reproducible execution
- strong caching and provenance
- strict policy enforcement
- scheduler portability
- schema-validated structured outputs
- parallel composite investigations

The model should separate user intent, execution planning, runtime state, artifacts, and reusable cache entries.

RAG compression adds a second axis to the model: local evidence processing before remote synthesis. The broker should represent manifests, chunks, indexes, retrieval runs, and evidence packs as first-class metadata even if the first implementation stores some of them as typed artifacts.

## Core Entities

### Job

Represents the externally visible lifecycle of a submitted task.

Suggested fields:

```json
{
  "id": "job_01...",
  "task_type": "log_analysis",
  "state": "running",
  "priority": "interactive",
  "submitted_by": "alice",
  "project_id": "proj_01...",
  "created_at": "2026-06-25T13:02:17Z",
  "updated_at": "2026-06-25T13:04:04Z",
  "submitted_at": "2026-06-25T13:02:17Z",
  "started_at": "2026-06-25T13:04:01Z",
  "completed_at": null,
  "parent_job_id": "job_parent_01...",
  "root_job_id": "job_root_01...",
  "retry_count": 0,
  "cache_status": "miss"
}
```

Additional composite-job fields:

- `parent_job_id`
- `root_job_id`
- `orchestration`

### JobSpec

Immutable normalized request used for execution and caching.

Suggested fields:

- normalized task input
- constraints
- output schema request
- policy context
- planner template version
- prompt/template version

Example:

```json
{
  "job_id": "job_01...",
  "spec_hash": "sha256:abc123...",
  "task_type": "log_analysis",
  "input_refs": ["input_01..."],
  "constraints": {
    "max_output_tokens": 4000,
    "max_runtime_seconds": 900,
    "confidentiality": "local_only"
  },
  "output_schema": {
    "name": "log_analysis_v1",
    "version": "1.0.0"
  },
  "planner_version": "2026-06-25",
  "template_version": "log-analysis@1.0.0"
}
```

### InputRef

Represents an immutable input reference.

Suggested fields:

- `id`
- `type`
- `uri`
- `content_hash`
- `size_bytes`
- `classification`
- `discovered_at`

Example classifications:

- `public`
- `internal`
- `restricted`
- `phi`
- `secret_adjacent`

### ExecutionPlan

Concrete execution details produced by the planner/router.

Suggested fields:

- `job_id`
- `backend_kind`
- `backend_queue`
- `qos`
- `resource_shape`
- `container_image`
- `model_name`
- `runtime_backend`
- `timeout_seconds`

Example:

```json
{
  "job_id": "job_01...",
  "backend_kind": "slurm",
  "qos": "preemptible",
  "resource_shape": {
    "cpus": 8,
    "memory_gb": 32,
    "gpus": 1
  },
  "container_image": "ghcr.io/org/broker-worker:qwen-coder",
  "model_name": "qwen-coder-large",
  "runtime_backend": "vllm",
  "timeout_seconds": 900
}
```

### BackendRun

Tracks the scheduler-specific execution instance.

Suggested fields:

- `job_id`
- `backend_kind`
- `backend_run_id`
- `state`
- `submitted_at`
- `started_at`
- `ended_at`
- `exit_code`
- `node_name`

This should be separate from `Job` so one logical broker job can survive retries or preemption.

Composite investigations may therefore have many `BackendRun` records under one `root_job_id`.

### Result

Validated terminal output from the worker.

Suggested fields:

- `job_id`
- `schema_name`
- `schema_version`
- `payload`
- `quality_signals`
- `evidence_refs`
- `provenance`

Example:

```json
{
  "job_id": "job_01...",
  "schema_name": "root_cause_analysis_v1",
  "schema_version": "1.0.0",
  "payload": {
    "summary": "Integration tests fail because the migration step did not run.",
    "top_hypotheses": [
      {
        "code": "MISSING_MIGRATION",
        "confidence": 0.88
      }
    ]
  },
  "quality_signals": {
    "model_confidence": 0.88,
    "input_coverage": 0.94
  },
  "evidence_refs": ["artifact_01..."],
  "provenance": {
    "model": "qwen-coder-large",
    "template_version": "rca@1.0.0"
  }
}
```

### Artifact

Blob or file-like output tied to a job.

Suggested fields:

- `id`
- `job_id`
- `type`
- `content_hash`
- `storage_uri`
- `size_bytes`
- `retention_class`
- `classification`

Artifact types may include:

- `redacted_log_excerpt`
- `structured_summary`
- `patch`
- `embedding_shard`
- `symbol_index`
- `chunk_manifest`
- `evidence_pack`
- `retrieval_result`
- `rerank_result`
- `execution_log`

### InputManifest

Canonical view of a local input scope after discovery.

Suggested fields:

- `id`
- `input_ref_ids`
- `corpus_hash`
- `file_count`
- `total_size_bytes`
- `classification`
- `commit_sha`
- `created_at`
- `artifact_id`

### Chunk

Evidence-addressable unit produced by chunking.

Suggested fields:

- `id`
- `manifest_id`
- `chunk_hash`
- `source_path`
- `line_start`
- `line_end`
- `byte_start`
- `byte_end`
- `timestamp_start`
- `timestamp_end`
- `commit_sha`
- `chunk_type`
- `token_estimate`
- `classification`

### LocalIndex

Local-only retrieval structure.

Suggested fields:

- `id`
- `manifest_id`
- `index_type`
- `strategy_version`
- `artifact_id`
- `chunk_count`
- `embedding_model`
- `created_at`
- `classification`

Index types:

- `ripgrep`
- `bm25`
- `tree_sitter_symbol`
- `embedding_vector`
- `stack_trace_path`
- `git_diff_history`

### RetrievalRun

Record of a query against local indexes.

Suggested fields:

- `id`
- `root_job_id`
- `query_hash`
- `manifest_ids`
- `index_ids`
- `strategies`
- `retrieved_chunk_ids`
- `reranked_chunk_ids`
- `retrieved_chunk_budget`
- `created_at`
- `cache_key`

### EvidencePack

Validated compressed evidence returned to the remote orchestrator.

Suggested fields:

- `id`
- `job_id`
- `root_job_id`
- `schema_name`
- `schema_version`
- `artifact_id`
- `query_hash`
- `source_manifest_ids`
- `evidence_count`
- `final_pack_tokens`
- `remote_context_budget`
- `policy_mode`
- `classification`
- `created_at`

### CacheEntry

Reusable result or intermediate indexed by content-derived key.

Suggested fields:

- `cache_key`
- `entry_type`
- `result_hash`
- `artifact_ids`
- `created_at`
- `expires_at`
- `provenance_hash`

Entry types:

- `final_result`
- `intermediate_parse`
- `embedding_index`
- `repo_summary`
- `chunk_manifest`
- `file_hash`
- `local_index`
- `retrieval_result`
- `rerank_result`
- `evidence_pack`
- `local_model_output`

### PolicyDecision

Recorded decision from the policy engine.

Suggested fields:

- `job_id`
- `decision`
- `reason_codes`
- `export_allowed`
- `redaction_required`
- `policy_version`

### AuditEvent

Append-only event log for high-trust actions.

Suggested fields:

- `id`
- `actor`
- `action`
- `resource_type`
- `resource_id`
- `timestamp`
- `metadata`

## State Model

Recommended logical job states:

- `accepted`
- `queued`
- `dispatching`
- `running`
- `succeeded`
- `failed`
- `cancelled`
- `timed_out`
- `preempted`
- `cache_hit`

The scheduler-native state should not be treated as the public API. Translate backend-specific states into stable broker states.

## Caching Model

The cache key should include:

- normalized task type
- input content hashes
- selection/chunking strategy
- retrieval strategy set
- reranker version
- compressor model and template version
- model logical name and version
- runtime backend version
- prompt or template version
- output schema version
- policy mode when relevant

This is necessary because identical source content can still produce non-equivalent outputs under different templates, models, or disclosure rules.

## Schema Strategy

All result payloads should be versioned JSON schemas.

Examples:

- `rag_evidence_pack_v1`
- `debug_evidence_pack_v1`
- `log_evidence_pack_v1`
- `repo_inspection_pack_v1`
- `patch_proposal_pack_v1`
- `repo_summary_v1`
- `log_analysis_v1`
- `root_cause_analysis_v1`
- `patch_generation_v1`
- `embedding_generation_v1`

Do not use free-form prose as the primary contract. Use prose only inside bounded fields such as:

- `summary`
- `recommended_next_steps`
- `patch_rationale`

## Multi-Tenancy Considerations

If the broker is shared across teams, the data model should also support:

- tenant or organization identifier
- project-level retention settings
- namespace-scoped cache visibility
- per-tenant policy bundles
- chargeback or usage attribution metadata

These fields should be included early in the model even if the first deployment is single-tenant.
