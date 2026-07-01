# Task And Result Schemas

## Purpose

The broker should expose a small, stable tool surface while supporting many task types. That only works if task inputs and outputs are defined through explicit schemas rather than ad hoc prompts and free-form prose.

This document defines:

- the schema strategy
- common envelope fields
- initial task types
- example result payload shapes

## Schema Design Principles

- `schema-first`: every worker returns a versioned JSON payload
- `compact by default`: results should be optimized for remote agent consumption, not human-readable bulk output
- `evidence-backed`: important claims should point to evidence artifacts
- `bounded prose`: prose is allowed in narrow fields such as summaries or rationales, not as the primary contract
- `forward-compatible`: clients should tolerate additive fields
- `policy-aware`: the broker may redact or omit fields before returning them to the MCP client

## Common Result Envelope

Every task should return a common outer envelope, regardless of task type.

```json
{
  "schema_name": "log_analysis_v1",
  "schema_version": "1.0.0",
  "payload": {},
  "evidence_refs": ["artifact_01..."],
  "quality_signals": {
    "model_confidence": 0.92,
    "input_coverage": 0.87
  },
  "provenance": {
    "model": "qwen-coder-large",
    "runtime_backend": "vllm",
    "template_version": "log-analysis@1.0.0"
  }
}
```

## Common Payload Conventions

Recommended shared fields across many task types:

- `summary`: short bounded summary
- `top_findings`: top structured findings
- `suggested_next_steps`: short action list
- `confidence`: finding-level or result-level confidence
- `evidence_refs`: artifact IDs supporting a finding
- `warnings`: limitations, truncation, or policy redactions

Recommended constraints:

- `summary` should usually be under 1-3 short paragraphs worth of text
- `top_findings` should be ranked
- `suggested_next_steps` should be short, actionable strings
- evidence should point to local artifacts rather than include raw content inline

## Input Schema Concepts

Each task request should carry:

- `task_type`
- `input_refs`
- `constraints`
- `execution_profile`
- `output_schema`

Task-specific request fields should live under:

- `task_params`

Example:

```json
{
  "task_type": "code_search",
  "input_refs": [
    {
      "type": "repo",
      "uri": "file:///workspace/repo",
      "content_hash": "sha256:abc123..."
    }
  ],
  "task_params": {
    "query": "all callers of submit_local_job",
    "languages": ["go", "python"],
    "max_matches": 200
  }
}
```

## Initial Task Types

The first task set should be deliberately narrow and high-value.

- `repo_summary`
- `code_search`
- `static_analysis`
- `log_analysis`
- `test_failure_analysis`
- `root_cause_analysis`
- `document_summary`
- `embedding_generation`
- `patch_generation`
- `rag_compress`
- `debug_with_local_context`
- `summarize_logs`
- `inspect_repo`

Parallel composition should work across these tasks, for example:

- many `document_summary` shard jobs feeding one summary aggregator
- many `log_analysis` shard jobs feeding one root-cause reducer
- many `repo_summary` or `code_search` jobs by subtree feeding one repository-wide summary

## `repo_summary_v1`

Purpose:

- summarize a repository or subdirectory without exporting raw source

Suggested payload:

```json
{
  "summary": "This repository provides a broker control plane, Slurm execution backend, and worker runtimes for local AI compute delegation.",
  "subsystems": [
    {
      "name": "broker",
      "role": "MCP control plane",
      "paths": ["broker/"],
      "confidence": 0.95
    }
  ],
  "entrypoints": [
    {
      "path": "cmd/broker-server/main.go",
      "kind": "service_entrypoint"
    }
  ],
  "dependencies": [
    {
      "name": "PostgreSQL",
      "kind": "runtime_dependency"
    }
  ],
  "risks": [
    "Current implementation is tightly coupled to Slurm-specific scripts."
  ],
  "evidence_refs": ["artifact_01_repo_manifest"]
}
```

## `rag_evidence_pack_v1`

Purpose:

- return compressed, evidence-backed local context for remote synthesis without exporting raw inputs

Suggested payload:

```json
{
  "query": "Why did the build fail?",
  "input_scope": {
    "classification": "restricted",
    "source_refs": ["repo:sha256:...", "log:sha256:..."]
  },
  "retrieval": {
    "strategies": ["ripgrep", "bm25", "stack_trace_path"],
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
```

Required evidence reference fields:

- `artifact_id` or `chunk_id`
- `path`, `timestamp_range`, `page_range`, or `commit` when applicable
- `content_hash` or `chunk_hash`

Raw excerpts are optional and policy-controlled.

## `debug_evidence_pack_v1`

Purpose:

- compress local debugging context from logs, stack traces, tests, code paths, and git history

Suggested payload:

```json
{
  "problem": "Unit tests fail after the authorization refactor.",
  "failure_signature": {
    "tests": ["TestGetJobForbiddenForDifferentActor"],
    "error_codes": ["FORBIDDEN"],
    "stack_trace_refs": ["artifact_stack_trace_cluster"]
  },
  "top_hypotheses": [
    {
      "code": "ROOT_AUTH_SCOPE_MISMATCH",
      "claim": "The root-level access check sees a mixed-owner root and denies the operation.",
      "confidence": 0.86,
      "evidence_refs": ["ev_001", "ev_002"]
    }
  ],
  "evidence": [],
  "suggested_local_followups": [
    {
      "tool": "rag_compress",
      "query": "Inspect root authorization checks for mixed-owner fanout jobs."
    }
  ]
}
```

## `log_evidence_pack_v1`

Purpose:

- compress large logs into deduplicated, timestamp-preserving evidence

Suggested payload:

```json
{
  "summary": "The first fatal error is a missing generated header; later failures are cascading compiler errors.",
  "timeline": [
    {
      "phase": "failure",
      "timestamp_hint": "2026-07-01T13:05:09Z",
      "evidence_refs": ["ev_001"]
    }
  ],
  "clusters": [
    {
      "kind": "repeated_error",
      "count": 138,
      "representative_evidence_ref": "ev_002"
    }
  ],
  "evidence": []
}
```

## `repo_inspection_pack_v1`

Purpose:

- return repository understanding from local code retrieval and evidence compression

Suggested payload:

```json
{
  "query": "Explain the MCP tool dispatch and authorization boundary.",
  "subsystems": [
    {
      "name": "MCP tool dispatch",
      "paths": ["broker/pkg/mcp/server.go"],
      "evidence_refs": ["ev_001"]
    }
  ],
  "symbols": [
    {
      "name": "callTool",
      "path": "broker/pkg/mcp/server.go",
      "line_start": 200,
      "line_end": 310,
      "evidence_refs": ["ev_002"]
    }
  ],
  "evidence": []
}
```

## `patch_proposal_pack_v1`

Purpose:

- propose candidate code changes from local evidence while preserving provenance and policy controls

Suggested payload:

```json
{
  "summary": "A minimal patch adds a root authorization check before releasing deferred chunks.",
  "patches": [
    {
      "patch_ref": "artifact_patch_diff",
      "paths": ["broker/pkg/service/service.go"],
      "rationale": "Matches existing root status authorization behavior.",
      "evidence_refs": ["ev_001", "ev_002"],
      "confidence": 0.78,
      "policy": {
        "diff_inline": false,
        "release_requires_approval": true
      }
    }
  ],
  "validation_steps": ["go test ./broker/pkg/service"]
}
```

## `code_search_v1`

Purpose:

- find code locations relevant to a query and summarize them compactly

Suggested payload:

```json
{
  "query": "all callers of submit_local_job",
  "matches": [
    {
      "path": "broker/pkg/mcp/tools.go",
      "symbol": "submitLocalJob",
      "kind": "function_call",
      "line_hint": 84,
      "excerpt_ref": "artifact_01_match_excerpt",
      "relevance": 0.97
    }
  ],
  "summary": "Most calls originate from MCP tool registration and integration tests.",
  "coverage": {
    "files_scanned": 241,
    "languages": ["go", "python"]
  }
}
```

## `static_analysis_v1`

Purpose:

- run static analysis and return ranked issues rather than raw tool dumps

Suggested payload:

```json
{
  "summary": "Three high-signal issues were found in scheduler reconciliation and artifact authorization paths.",
  "issues": [
    {
      "code": "MISSING_AUTHZ_CHECK",
      "severity": "high",
      "path": "broker/pkg/api/results.go",
      "line_hint": 117,
      "description": "Result retrieval path lacks project-level authorization enforcement.",
      "evidence_refs": ["artifact_01_static_issue"],
      "confidence": 0.91
    }
  ],
  "tooling": [
    {
      "name": "semgrep",
      "version": "x.y.z"
    }
  ]
}
```

## `log_analysis_v1`

Purpose:

- compress large logs into structured findings and next steps

Suggested payload:

```json
{
  "summary": "The build fails during generated header resolution after the codegen step is skipped.",
  "top_findings": [
    {
      "code": "MISSING_GENERATED_HEADER",
      "severity": "high",
      "confidence": 0.92,
      "evidence_refs": ["artifact_01_log_excerpt"]
    }
  ],
  "timeline": [
    {
      "phase": "build_start",
      "timestamp_hint": "2026-06-25T13:04:11Z"
    },
    {
      "phase": "failure",
      "timestamp_hint": "2026-06-25T13:05:09Z"
    }
  ],
  "suggested_next_steps": [
    "Run the code generation target before invoking compile.",
    "Confirm generated include paths are present in the build environment."
  ]
}
```

## `test_failure_analysis_v1`

Purpose:

- connect test failures to likely causes and relevant code locations

Suggested payload:

```json
{
  "summary": "Integration tests fail because the test database migration hook did not run.",
  "failing_tests": [
    {
      "name": "TestBrokerResultFetch",
      "file": "tests/integration/results_test.py",
      "failure_mode": "unexpected_404"
    }
  ],
  "top_hypotheses": [
    {
      "code": "MISSING_DB_MIGRATION",
      "confidence": 0.88,
      "evidence_refs": ["artifact_01_test_excerpt", "artifact_02_schema_diff"]
    }
  ],
  "related_paths": [
    "broker/pkg/store/migrations/",
    "tests/integration/"
  ]
}
```

## `root_cause_analysis_v1`

Purpose:

- synthesize multiple sources into ranked root-cause hypotheses

Suggested payload:

```json
{
  "summary": "The broker accepted a job but the worker never ran because Slurm submission succeeded while container staging failed on the compute node.",
  "top_hypotheses": [
    {
      "code": "CONTAINER_STAGE_FAILURE",
      "confidence": 0.9,
      "supporting_signals": [
        "backend job exists",
        "worker heartbeat never observed",
        "staging error present in scheduler log"
      ],
      "evidence_refs": ["artifact_01_scheduler_log"]
    }
  ],
  "ruled_out_hypotheses": [
    "Policy denial",
    "Queue timeout"
  ],
  "suggested_next_steps": [
    "Verify OCI image pull access on compute nodes.",
    "Retry with debug staging logs enabled."
  ]
}
```

## `document_summary_v1`

Purpose:

- summarize large local documents without exposing the document body remotely

Suggested payload:

```json
{
  "summary": "The document proposes a broker-mediated architecture for delegating token-heavy tasks to local Slurm-managed GPUs.",
  "sections": [
    {
      "title": "Security Model",
      "summary": "Raw data remains local unless explicit export is requested."
    }
  ],
  "key_points": [
    "Use MCP as the northbound protocol.",
    "Run compute as ordinary preemptible cluster jobs."
  ],
  "open_questions": [
    "Should policy enforcement be embedded or delegated to OPA?"
  ]
}
```

## `embedding_generation_v1`

Purpose:

- create reusable local embeddings and return metadata, not vectors inline

Suggested payload:

```json
{
  "summary": "Embeddings generated for 14,221 chunks across 3 repositories.",
  "index_ref": "artifact_01_embedding_index",
  "embedding_model": "bge-large",
  "chunk_count": 14221,
  "dimensions": 1024,
  "coverage": {
    "repos": 3,
    "files": 941
  }
}
```

## `patch_generation_v1`

Purpose:

- return candidate fixes in a structured package

Suggested payload:

```json
{
  "summary": "A minimal fix adds authorization checks before result artifact retrieval.",
  "patches": [
    {
      "patch_ref": "artifact_01_patch_diff",
      "paths": ["broker/pkg/api/results.go"],
      "rationale": "Align result fetch path with existing project-scoped authorization logic.",
      "confidence": 0.79
    }
  ],
  "validation_steps": [
    "Run integration tests for result retrieval authorization.",
    "Verify shared project access still functions."
  ],
  "risks": [
    "May require additional changes in admin-only result paths."
  ]
}
```

## Artifact Schema Conventions

Artifacts should be typed and referenced by ID.

Recommended artifact types:

- `excerpt`
- `redacted_excerpt`
- `patch_diff`
- `chunk_manifest`
- `symbol_index`
- `embedding_index`
- `execution_log`
- `result_blob`

Artifacts should support:

- `artifact_id`
- `artifact_type`
- `content_hash`
- `classification`
- `storage_uri`

## Versioning Strategy

Rules:

- bump patch version for clarifications that do not change machine semantics
- bump minor version for additive backward-compatible fields
- bump major version for breaking structural changes

Do not overload one schema name with incompatible payloads. Prefer:

- `log_analysis_v1`
- `log_analysis_v2`

Instead of silently changing the meaning of `log_analysis_v1`.

## Policy And Redaction Behavior

The worker result is not necessarily identical to the MCP-visible result.

The broker may:

- remove fields
- replace fields with redacted markers
- swap raw excerpts for artifact references
- block entire result delivery

That means schemas should be designed so redaction is still representable. For example:

- optional `excerpt_ref` instead of inline excerpt text
- optional `warnings` field to explain omissions

## Guidance For Adding New Tasks

A new task type should not be added unless:

- it has a clear local-compute value
- it can return compact structured outputs
- it has a stable schema
- it can be scheduled and cached predictably
- it does not require raw remote disclosure by default

Each new task should define:

- request params
- result schema
- evidence strategy
- cache key inputs
- policy considerations
