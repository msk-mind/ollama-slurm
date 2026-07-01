# MCP Tools And Broker API

## Goals

The MCP surface should be small, stable, and scheduler-agnostic.

The core user story is not "start a model server." It is "delegate a local computation task and retrieve a compact result." Tool definitions should reflect that boundary.

## Broker HTTP API

The HTTP API should stay aligned with the MCP surface, with a small number of operator-oriented endpoints added where needed.

Current implementation includes:

- `GET /healthz`
- `POST /v1/jobs`
- `GET /v1/jobs`
- `GET /v1/jobs/{id}`
- `GET /v1/roots/{root_job_id}`
- `GET /v1/jobs/{id}/result`
- `GET /v1/jobs/{id}/logs`
- `POST /v1/jobs/{id}:cancel`
- `GET /v1/system/audit-health`

Target RAG compression endpoints:

- `POST /v1/rag/compressions`
- `POST /v1/rag/debug-sessions`
- `POST /v1/logs:summarize`
- `POST /v1/repos:inspect`
- `POST /v1/patches:propose`
- `GET /v1/rag/evidence-packs/{artifact_id}/metadata`
- `GET /v1/rag/indexes/{artifact_id}/metadata`
- `POST /v1/rag/cache:lookup`

These endpoints should be convenience aliases over normal broker jobs, not a separate lifecycle system. Status, result retrieval, cancellation, caching, audit, and policy enforcement should continue to use the standard job model.

Endpoint contract:

| Endpoint | Task type | Request body | Success response |
| --- | --- | --- | --- |
| `POST /v1/jobs` | caller-supplied `task_type` | generic job request envelope | async broker job response |
| `POST /v1/rag/compressions` | `rag_compress` | `rag_compress` input schema | async broker job response with `result_schema=rag_evidence_pack_v1` |
| `POST /v1/rag/debug-sessions` | `debug_with_local_context` | `debug_with_local_context` input schema | async broker job response with `result_schema=debug_evidence_pack_v1` |
| `POST /v1/logs:summarize` | `summarize_logs` | `summarize_logs` input schema | async broker job response with `result_schema=log_evidence_pack_v1` |
| `POST /v1/repos:inspect` | `inspect_repo` | `inspect_repo` input schema | async broker job response with `result_schema=repo_inspection_pack_v1` |
| `POST /v1/patches:propose` | `propose_patch` | `propose_patch` input schema | async broker job response with `result_schema=patch_proposal_pack_v1` |
| `GET /v1/jobs/{id}` | lifecycle | path parameter `id` | job status response |
| `GET /v1/jobs/{id}/result` | lifecycle | path parameter `id`, optional query `include_artifacts=false` | broker-filtered release view |
| `GET /v1/jobs/{id}/logs` | lifecycle | path parameter `id`, optional query `stream`, `max_bytes` | redacted worker log view |
| `POST /v1/jobs/{id}:cancel` | lifecycle | optional cancellation reason | cancelled job response |

### Status And Result Quality Fields

Both `GET /v1/jobs/{id}` and `GET /v1/jobs/{id}/result` expose the broker's local-execution quality summary:

- `runtime_diagnostics`
  - detailed broker-ingested runtime summary
  - excludes raw runtime endpoint URLs
- `execution_quality`
  - `real_local`
  - `degraded_local`
  - `no_real_backend`
- `degraded_local_execution`
  - explicit boolean summary for degraded local execution
- `retry_recommended`
  - explicit boolean summary indicating the broker recommends retrying on a stronger or real local backend

Recommended client behavior:

- branch first on `execution_quality`
- use `retry_recommended` for direct escalation logic
- inspect `runtime_diagnostics.last_error` only when an operator or agent needs the failure cause

Example: healthy local execution

```json
{
  "runtime_diagnostics": {
    "backend_name": "llama.cpp",
    "backend_mode": "real",
    "selected_model": "gpt-oss-20b.p40",
    "resource_tier": "p40-rag-compression",
    "endpoint_configured": true,
    "llm_available": true,
    "timeout_seconds": 10
  },
  "execution_quality": "real_local",
  "degraded_local_execution": false,
  "retry_recommended": false
}
```

Example: degraded local fallback

```json
{
  "runtime_diagnostics": {
    "backend_name": "llama.cpp",
    "backend_mode": "unavailable",
    "selected_model": "gpt-oss-20b.p40",
    "resource_tier": "p40-rag-compression",
    "endpoint_configured": true,
    "llm_available": false,
    "timeout_seconds": 10,
    "last_error": "<urlopen error timed out>"
  },
  "execution_quality": "degraded_local",
  "degraded_local_execution": true,
  "retry_recommended": false
}
```

Example: no real backend, retry recommended

```json
{
  "runtime_diagnostics": {
    "backend_name": "deterministic",
    "backend_mode": "heuristic",
    "endpoint_configured": false,
    "llm_available": false,
    "timeout_seconds": 20
  },
  "execution_quality": "no_real_backend",
  "degraded_local_execution": true,
  "retry_recommended": true
}
```

| `GET /v1/roots/{root_job_id}` | lifecycle | path parameter `root_job_id` | aggregate root investigation status |
| `GET /v1/rag/evidence-packs/{artifact_id}/metadata` | metadata | path parameter `artifact_id` | evidence-pack metadata only |
| `GET /v1/rag/indexes/{artifact_id}/metadata` | metadata | path parameter `artifact_id` | local-index metadata only |
| `POST /v1/rag/cache:lookup` | cache | cache lookup query with namespace, type, and key material | cache metadata without raw payloads |

Operator endpoint notes:

- `GET /v1/system/audit-health` is not an MCP end-user tool
- it is intended for admin health checks and operational automation
- it returns current audit-chain verification status for the active audit file

## MCP Session Identity

The MCP session should carry an explicit broker principal.

Recommended `initialize` params:

```json
{
  "auth": {
    "actor": "alice",
    "role": "user"
  }
}
```

Rules:

- the broker should bind a single principal to the MCP session during `initialize`
- all subsequent tool calls should execute as that principal
- unattended local sessions may fall back to broker-side configuration such as `BROKER_MCP_ACTOR`
- if no session identity exists, tool execution should fail rather than silently defaulting to an implicit broad principal

## Core MCP Tools

The broker has two tool layers:

- generic lifecycle tools for all broker jobs
- first-class RAG compression tools for evidence-pack workflows

RAG tools still create normal broker jobs. Clients should use `get_job_status`, `fetch_result`, and `cancel_job` for lifecycle management.

## RAG Compression MCP Tools

Exact request and response schemas for the RAG tools are defined in [RAG Compression](./rag-compression.md).

The first-class RAG tools are:

- `rag_compress`: produce `rag_evidence_pack_v1` from local inputs and a query
- `debug_with_local_context`: produce `debug_evidence_pack_v1` for failures using logs, stack traces, repo paths, tests, and git history
- `summarize_logs`: produce `log_evidence_pack_v1` from local log files without exporting raw logs
- `inspect_repo`: produce `repo_inspection_pack_v1` from local repository indexes and compressed code evidence
- `propose_patch`: produce `patch_proposal_pack_v1` from local evidence packs and authorized repo paths

All RAG tool requests support:

- `input_refs`
- `task_params`
- `constraints.retrieved_chunk_budget`
- `constraints.per_chunk_compression_budget`
- `constraints.final_evidence_pack_budget`
- `constraints.remote_model_context_budget`
- `execution_profile.tier`
- `idempotency_key`

Default execution tiers:

- `cpu-rag-indexing` for discovery, hashing, chunking, lexical indexing, tree-sitter parsing, and deduplication
- `p40-rag-compression` for routine embedding, reranking, and evidence compression
- `a100-reasoning` for patch generation, hard reasoning, and large-context compression

## Exact RAG MCP Schemas

The canonical MCP registration should use JSON Schema for `inputSchema`. For readability, shared fragments are shown once here; implementations should inline or merge them into each registered tool schema.

Shared fragments:

```json
{
  "$defs": {
    "InputRef": {
      "type": "object",
      "additionalProperties": false,
      "required": ["type", "uri"],
      "properties": {
        "type": {
          "type": "string",
          "enum": ["repo", "file", "log", "document", "artifact", "git_range", "stdin"]
        },
        "uri": {
          "type": "string",
          "minLength": 1
        },
        "content_hash": {
          "type": "string",
          "pattern": "^sha256:[a-fA-F0-9]{64}$"
        },
        "classification": {
          "type": "string",
          "enum": ["public", "internal", "restricted", "phi", "secret_adjacent"]
        },
        "metadata": {
          "type": "object",
          "additionalProperties": true
        }
      }
    },
    "RagConstraints": {
      "type": "object",
      "additionalProperties": false,
      "properties": {
        "retrieved_chunk_budget": {
          "type": "integer",
          "minimum": 1024
        },
        "per_chunk_compression_budget": {
          "type": "integer",
          "minimum": 64
        },
        "final_evidence_pack_budget": {
          "type": "integer",
          "minimum": 512
        },
        "remote_model_context_budget": {
          "type": "integer",
          "minimum": 1024
        },
        "max_runtime_seconds": {
          "type": "integer",
          "minimum": 1
        },
        "confidentiality": {
          "type": "string",
          "enum": ["local_only", "evidence_only", "redacted_summary", "raw_allowed_with_approval"]
        }
      }
    },
    "ExecutionProfile": {
      "type": "object",
      "additionalProperties": false,
      "properties": {
        "backend": {
          "type": "string",
          "enum": ["slurm", "local", "kubernetes", "ray", "standalone"]
        },
        "tier": {
          "type": "string",
          "enum": ["cpu-rag-indexing", "p40-rag-compression", "a100-reasoning"]
        },
        "model": {
          "type": "string",
          "minLength": 1
        },
        "runtime": {
          "type": "string",
          "enum": ["llama.cpp", "vllm", "sglang", "deterministic"]
        },
        "qos": {
          "type": "string",
          "enum": ["preemptible", "low", "normal", "interactive"]
        }
      }
    },
    "AsyncJobResponse": {
      "type": "object",
      "additionalProperties": false,
      "required": ["job_id", "state", "result_schema", "status_url"],
      "properties": {
        "job_id": {
          "type": "string"
        },
        "state": {
          "type": "string",
          "enum": ["accepted", "queued", "dispatching", "running", "succeeded", "failed", "cancelled"]
        },
        "result_schema": {
          "type": "string"
        },
        "cache": {
          "type": "object",
          "additionalProperties": false,
          "properties": {
            "status": {
              "type": "string",
              "enum": ["hit", "miss", "bypass", "refresh"]
            },
            "cache_key": {
              "type": "string"
            }
          }
        },
        "status_url": {
          "type": "string"
        },
        "result_url": {
          "type": "string"
        }
      }
    }
  }
}
```

`rag_compress`:

```json
{
  "name": "rag_compress",
  "description": "Compress authorized local inputs into a compact evidence pack for remote synthesis without exporting raw data by default.",
  "inputSchema": {
    "type": "object",
    "additionalProperties": false,
    "required": ["query", "input_refs"],
    "properties": {
      "query": {
        "type": "string",
        "minLength": 1
      },
      "input_refs": {
        "type": "array",
        "minItems": 1,
        "items": {
          "$ref": "#/$defs/InputRef"
        }
      },
      "task_params": {
        "type": "object",
        "additionalProperties": false,
        "properties": {
          "retrieval_strategies": {
            "type": "array",
            "items": {
              "type": "string",
              "enum": ["ripgrep", "bm25", "tree_sitter", "embeddings", "stack_trace_path", "git_diff_history"]
            }
          },
          "include_inline_excerpts": {
            "type": "boolean",
            "default": false
          },
          "evidence_kinds": {
            "type": "array",
            "items": {
              "type": "string"
            }
          },
          "paths": {
            "type": "array",
            "items": {
              "type": "string"
            }
          }
        }
      },
      "constraints": {
        "$ref": "#/$defs/RagConstraints"
      },
      "execution_profile": {
        "$ref": "#/$defs/ExecutionProfile"
      },
      "idempotency_key": {
        "type": "string"
      }
    }
  },
  "outputSchema": {
    "$ref": "#/$defs/AsyncJobResponse"
  }
}
```

`debug_with_local_context`:

```json
{
  "name": "debug_with_local_context",
  "description": "Run a local debugging RAG workflow over logs, stack traces, tests, repo paths, and git history.",
  "inputSchema": {
    "type": "object",
    "additionalProperties": false,
    "required": ["problem", "input_refs"],
    "properties": {
      "problem": {
        "type": "string",
        "minLength": 1
      },
      "input_refs": {
        "type": "array",
        "minItems": 1,
        "items": {
          "$ref": "#/$defs/InputRef"
        }
      },
      "task_params": {
        "type": "object",
        "additionalProperties": false,
        "properties": {
          "stack_trace": {
            "type": "string"
          },
          "failing_tests": {
            "type": "array",
            "items": {
              "type": "string"
            }
          },
          "changed_since": {
            "type": "string"
          },
          "suspect_paths": {
            "type": "array",
            "items": {
              "type": "string"
            }
          },
          "retrieval_strategies": {
            "type": "array",
            "items": {
              "type": "string",
              "enum": ["ripgrep", "bm25", "tree_sitter", "embeddings", "stack_trace_path", "git_diff_history"]
            }
          }
        }
      },
      "constraints": {
        "$ref": "#/$defs/RagConstraints"
      },
      "execution_profile": {
        "$ref": "#/$defs/ExecutionProfile"
      },
      "idempotency_key": {
        "type": "string"
      }
    }
  },
  "outputSchema": {
    "$ref": "#/$defs/AsyncJobResponse"
  }
}
```

`summarize_logs`:

```json
{
  "name": "summarize_logs",
  "description": "Retrieve, cluster, deduplicate, and compress large local logs into timestamp-preserving evidence.",
  "inputSchema": {
    "type": "object",
    "additionalProperties": false,
    "required": ["input_refs"],
    "properties": {
      "input_refs": {
        "type": "array",
        "minItems": 1,
        "items": {
          "$ref": "#/$defs/InputRef"
        }
      },
      "task_params": {
        "type": "object",
        "additionalProperties": false,
        "properties": {
          "query": {
            "type": "string"
          },
          "time_window": {
            "type": "object",
            "additionalProperties": false,
            "properties": {
              "start": {
                "type": "string",
                "format": "date-time"
              },
              "end": {
                "type": "string",
                "format": "date-time"
              }
            }
          },
          "deduplicate_repeated_lines": {
            "type": "boolean",
            "default": true
          },
          "cluster_stack_traces": {
            "type": "boolean",
            "default": true
          }
        }
      },
      "constraints": {
        "$ref": "#/$defs/RagConstraints"
      },
      "execution_profile": {
        "$ref": "#/$defs/ExecutionProfile"
      },
      "idempotency_key": {
        "type": "string"
      }
    }
  },
  "outputSchema": {
    "$ref": "#/$defs/AsyncJobResponse"
  }
}
```

`inspect_repo`:

```json
{
  "name": "inspect_repo",
  "description": "Build or reuse local repository indexes and return compressed code evidence for a question.",
  "inputSchema": {
    "type": "object",
    "additionalProperties": false,
    "required": ["input_refs", "task_params"],
    "properties": {
      "input_refs": {
        "type": "array",
        "minItems": 1,
        "items": {
          "$ref": "#/$defs/InputRef"
        }
      },
      "task_params": {
        "type": "object",
        "additionalProperties": false,
        "required": ["query"],
        "properties": {
          "query": {
            "type": "string",
            "minLength": 1
          },
          "paths": {
            "type": "array",
            "items": {
              "type": "string"
            }
          },
          "languages": {
            "type": "array",
            "items": {
              "type": "string"
            }
          },
          "retrieval_strategies": {
            "type": "array",
            "items": {
              "type": "string",
              "enum": ["ripgrep", "bm25", "tree_sitter", "embeddings", "stack_trace_path", "git_diff_history"]
            }
          },
          "include_dependency_edges": {
            "type": "boolean",
            "default": false
          }
        }
      },
      "constraints": {
        "$ref": "#/$defs/RagConstraints"
      },
      "execution_profile": {
        "$ref": "#/$defs/ExecutionProfile"
      },
      "idempotency_key": {
        "type": "string"
      }
    }
  },
  "outputSchema": {
    "$ref": "#/$defs/AsyncJobResponse"
  }
}
```

`propose_patch`:

```json
{
  "name": "propose_patch",
  "description": "Generate a local candidate patch package from evidence packs and authorized repository paths.",
  "inputSchema": {
    "type": "object",
    "additionalProperties": false,
    "required": ["problem", "input_refs"],
    "properties": {
      "problem": {
        "type": "string",
        "minLength": 1
      },
      "input_refs": {
        "type": "array",
        "minItems": 1,
        "items": {
          "$ref": "#/$defs/InputRef"
        }
      },
      "task_params": {
        "type": "object",
        "additionalProperties": false,
        "properties": {
          "patch_style": {
            "type": "string",
            "enum": ["minimal", "surgical", "refactor", "test_only"]
          },
          "allowed_paths": {
            "type": "array",
            "items": {
              "type": "string"
            }
          },
          "validation_commands": {
            "type": "array",
            "items": {
              "type": "string"
            }
          },
          "include_inline_diff": {
            "type": "boolean",
            "default": false
          }
        }
      },
      "constraints": {
        "$ref": "#/$defs/RagConstraints"
      },
      "execution_profile": {
        "$ref": "#/$defs/ExecutionProfile"
      },
      "idempotency_key": {
        "type": "string"
      }
    }
  },
  "outputSchema": {
    "$ref": "#/$defs/AsyncJobResponse"
  }
}
```

Lifecycle tools used by RAG flows:

```json
[
  {
    "name": "get_job_status",
    "description": "Return broker state, backend state, safe progress, and orchestration metadata for one job.",
    "inputSchema": {
      "type": "object",
      "additionalProperties": false,
      "required": ["job_id"],
      "properties": {
        "job_id": {
          "type": "string",
          "minLength": 1
        }
      }
    }
  },
  {
    "name": "fetch_result",
    "description": "Return the broker-filtered release view for a terminal job result.",
    "inputSchema": {
      "type": "object",
      "additionalProperties": false,
      "required": ["job_id"],
      "properties": {
        "job_id": {
          "type": "string",
          "minLength": 1
        },
        "include_artifacts": {
          "type": "boolean",
          "default": false
        },
        "release_mode": {
          "type": "string",
          "enum": ["evidence_only", "metadata_only", "redacted", "raw_with_approval"],
          "default": "evidence_only"
        }
      }
    }
  },
  {
    "name": "cancel_job",
    "description": "Cancel a queued, dispatching, or running broker job and its backend job when possible.",
    "inputSchema": {
      "type": "object",
      "additionalProperties": false,
      "required": ["job_id"],
      "properties": {
        "job_id": {
          "type": "string",
          "minLength": 1
        },
        "reason": {
          "type": "string"
        }
      }
    }
  }
]
```

### `submit_local_job`

Submit a local analysis or inference task.

Example request:

```json
{
  "task_type": "log_analysis",
  "input_refs": [
    {
      "type": "file",
      "uri": "file:///workspace/build.log",
      "content_hash": "sha256:6f6d..."
    }
  ],
  "constraints": {
    "max_input_tokens": 2000000,
    "max_output_tokens": 4000,
    "max_runtime_seconds": 900,
    "priority": "interactive",
    "confidentiality": "local_only",
    "allow_remote_escalation": false
  },
  "execution_profile": {
    "backend": "slurm",
    "model": "qwen-coder-large",
    "accelerator": "gpu",
    "qos": "preemptible"
  },
  "orchestration": {
    "root_job_id": "job_root_01jz...",
    "parent_job_id": "job_parent_01jz...",
    "strategy": "fanout_child",
    "shard_key": "repo:src",
    "shard_index": 2,
    "shard_count": 8,
    "aggregation_key": "repo-summary-pass-1"
  },
  "output_schema": {
    "name": "log_analysis_v1"
  },
  "idempotency_key": "optional-client-key"
}
```

Behavior:

- validates task and schema
- evaluates policy
- checks cache
- creates or reuses a job
- returns broker job metadata

Parallel orchestration semantics:

- clients may submit many child jobs concurrently with the existing `submit_local_job` tool
- child jobs should share a `root_job_id`
- `parent_job_id` identifies the immediate parent investigation or reducer
- reducers and validators should use a non-`fanout_child` strategy label

### `submit_parallel_jobs`

Submit a batch of child jobs under one logical root investigation.

Example request:

```json
{
  "task_type": "repo_summary",
  "execution_profile": {
    "backend": "slurm",
    "qos": "preemptible"
  },
  "output_schema": {
    "name": "repo_summary_v1"
  },
  "children": [
    {
      "input_refs": [
        {
          "type": "repo",
          "uri": "file:///workspace/repo"
        }
      ],
      "shard_key": "repo:src",
      "shard_index": 0,
      "shard_count": 4
    },
    {
      "input_refs": [
        {
          "type": "repo",
          "uri": "file:///workspace/repo"
        }
      ],
      "shard_key": "repo:test",
      "shard_index": 1,
      "shard_count": 4
    }
  ]
}
```

Example response:

```json
{
  "root_job_id": "root_01jz...",
  "strategy": "fanout_child",
  "child_count": 2,
  "children": [
    {
      "job_id": "job_01a...",
      "state": "queued",
      "status_url": "/v1/jobs/job_01a...",
      "shard_key": "repo:src",
      "shard_index": 0,
      "shard_count": 4
    },
    {
      "job_id": "job_01b...",
      "state": "queued",
      "status_url": "/v1/jobs/job_01b...",
      "shard_key": "repo:test",
      "shard_index": 1,
      "shard_count": 4
    }
  ]
}
```

Current semantics:

- this is a broker-side batch helper, not a server-side DAG scheduler
- the broker preserves one normal broker job per child
- homogeneous uncached child shards may be submitted through a backend batch primitive such as one Slurm job array
- very large uncached shard sets may be chunked into multiple bounded backend batches under one root investigation
- when root throttling is enabled, later child chunks may remain in broker `dispatching` state until earlier chunks complete
- cache hits are still materialized as independent broker jobs and are not forced into a scheduler batch
- all children share one `root_job_id`
- callers still decide shard layout and any later reducer or validator jobs

Optional reducer semantics:

- the request may include a `reducer` object
- the broker will submit one additional aggregator job after child submission
- when deferred child chunks exist, the reducer may also begin in `dispatching` and submit later once child dispatch completes
- the reducer job receives `child_job_ids` in `task_params`
- current reducer coordination is worker-driven: the reducer waits for child result files rather than relying on backend-native dependencies
- for Slurm command-mode backends, reducer submission also carries native `afterany` dependencies on the child scheduler jobs

Example response:

```json
{
  "job_id": "job_01jz4f8j6q0j4w3p7gx4h0n1jv",
  "state": "accepted",
  "cache": {
    "status": "miss"
  },
  "status_url": "/v1/jobs/job_01jz4f8j6q0j4w3p7gx4h0n1jv"
}
```

### `get_job_status`

Retrieve current job state and progress.

Request:

```json
{
  "job_id": "job_01jz4f8j6q0j4w3p7gx4h0n1jv"
}
```

Response:

```json
{
  "job_id": "job_01jz4f8j6q0j4w3p7gx4h0n1jv",
  "state": "running",
  "submitted_at": "2026-06-25T13:02:17Z",
  "started_at": "2026-06-25T13:04:01Z",
  "progress": {
    "state": "running",
    "phase": "analyzing_chunks",
    "percent": 42,
    "message": "Summarizing staged repository chunks",
    "timestamp": "2026-06-25T13:05:04Z",
    "last_updated": "2026-06-25T13:05:04Z",
    "metrics": {
      "chunks_processed": 84,
      "chunks_total": 200
    }
  },
  "backend_kind": "slurm",
  "backend_run_id": "8123456",
  "backend_state": "RUNNING",
  "runtime_diagnostics": {
    "backend_name": "llama.cpp",
    "backend_mode": "real",
    "selected_model": "gpt-oss-20b.p40",
    "resource_tier": "p40-rag-compression",
    "endpoint_configured": true,
    "llm_available": true,
    "timeout_seconds": 10
  },
  "execution_quality": "real_local",
  "degraded_local_execution": false,
  "retry_recommended": false,
  "parent_job_id": "job_parent_01jz...",
  "root_job_id": "job_root_01jz...",
  "orchestration": {
    "strategy": "fanout_child",
    "shard_index": 2,
    "shard_count": 8
  }
}
```

Progress semantics:

- `progress.state` is worker-reported liveness and may be more granular than broker `state`
- `phase` is meant for orchestration and UX decisions, not strict scheduling guarantees
- `percent` is best-effort and may advance in coarse steps for deterministic workers
- `metrics` should remain compact, structured, and safe to return to a remote orchestrator
- if `progress` is absent, no worker heartbeat has been observed yet

### `get_root_job_status`

Retrieve aggregate status for one logical investigation spanning many shard and reducer jobs.

Request:

```json
{
  "root_job_id": "root_01jz..."
}
```

Response:

```json
{
  "root_job_id": "root_01jz...",
  "state": "running",
  "total_jobs": 5,
  "queued_jobs": 1,
  "running_jobs": 1,
  "succeeded_jobs": 3,
  "failed_jobs": 0,
  "cancelled_jobs": 0,
  "dispatching_children": 2,
  "pending_children": 2,
  "active_chunks": 1,
  "pending_chunks": 1,
  "reducer_job_id": "job_reduce_01",
  "reducer_state": "running",
  "child_job_ids": ["job_a", "job_b", "job_c", "job_d"]
}
```

Current semantics:

- aggregate state is broker-derived from the visible child and reducer jobs
- only the effective latest attempt per shard or reducer stream contributes to root state
- reducer state dominates when a reducer job exists
- `dispatching_children` counts child jobs currently held in broker-managed dispatch state
- `pending_children` counts `dispatching` children that have not yet been submitted to a backend
- `active_chunks` counts child chunks currently occupying backend submission slots
- `pending_chunks` counts deferred child chunks not yet released
- `reducer_deferred` indicates that the reducer exists only as a broker placeholder and has not yet been submitted
- this is the intended poll target for client-side fan-out workflows

### `retry_failed_root_shards`

Retry only the failed current shard attempts for an existing root investigation.

Request:

```json
{
  "root_job_id": "root_01jz...",
  "include_cancelled": false,
  "resubmit_reducer": true
}
```

Response:

```json
{
  "root_job_id": "root_01jz...",
  "retried_count": 2,
  "cumulative_retried_shards": 3,
  "remaining_retried_shard_budget": 1,
  "retried_shards": [
    {
      "previous_job_id": "job_failed_b",
      "job_id": "job_retry_b",
      "state": "queued",
      "status_url": "/v1/jobs/job_retry_b",
      "shard_index": 1,
      "shard_count": 4
    }
  ],
  "skipped_count": 2,
  "reducer_job": {
    "job_id": "job_reduce_retry",
    "state": "queued",
    "status_url": "/v1/jobs/job_reduce_retry"
  }
}
```

Current semantics:

- the broker groups shard attempts by root and shard identity
- shards that already have any successful attempt are not retried
- queued or running latest attempts are not retried
- failed, preempted, and timed-out latest attempts are retried
- cancelled latest attempts are retried only when `include_cancelled` is true
- non-admin callers are capped by `BROKER_ROOT_ACTION_MAX_RETRIED_SHARDS`
- the non-admin retry cap is cumulative for the root, not only per call
- successful responses report `cumulative_retried_shards` directly
- non-admin successful responses also report `remaining_retried_shard_budget`
- admin callers may exceed the non-admin cap
- when `resubmit_reducer` is true and a reducer exists, the broker submits a fresh reducer over the current effective shard set

### `release_deferred_root_chunks`

Force immediate release of deferred child chunks for an existing root investigation.

Request:

```json
{
  "root_job_id": "root_01jz...",
  "max_additional_batches": 1
}
```

Response:

```json
{
  "root_job_id": "root_01jz...",
  "released_chunks": 1,
  "released_children": 2,
  "reducer_released": false,
  "cumulative_forced_release_chunks": 2,
  "remaining_forced_release_budget": 1,
  "root_status": {
    "root_job_id": "root_01jz...",
    "active_chunks": 2,
    "pending_chunks": 1
  }
}
```

Current semantics:

- this is a one-shot control-plane action, not a permanent per-root policy change
- `max_additional_batches` overrides the broker’s normal active-batch limit only for this release call
- non-admin callers are capped by `BROKER_ROOT_ACTION_MAX_ADDITIONAL_BATCHES`
- the non-admin forced-release cap is cumulative for the root, not only per call
- successful responses report `cumulative_forced_release_chunks` directly
- non-admin successful responses also report `remaining_forced_release_budget`
- admin callers may exceed the non-admin cap
- the broker submits up to the requested number of deferred child chunks immediately
- if no deferred child chunks remain after release, a deferred reducer may also be submitted in place
- the response includes updated root status so the caller can decide whether another release is warranted

### `fetch_result`

Retrieve terminal job result and compact artifacts.

Request:

```json
{
  "job_id": "job_01jz4f8j6q0j4w3p7gx4h0n1jv",
  "include_artifacts": false
}
```

Response:

```json
{
  "job_id": "job_01jz4f8j6q0j4w3p7gx4h0n1jv",
  "state": "succeeded",
  "result": {
    "schema_name": "log_analysis_v1",
    "schema_version": "1.0.0",
    "payload": {
      "summary": "Compilation fails because generated headers are missing.",
      "top_findings": [
        {
          "code": "MISSING_GENERATED_HEADER",
          "confidence": 0.92,
          "evidence_refs": ["artifact_01..."]
        }
      ],
      "suggested_next_steps": [
        "Run the code generation step before compiling.",
        "Verify include paths for generated output."
      ]
    }
  },
  "runtime_diagnostics": {
    "backend_name": "deterministic",
    "backend_mode": "heuristic",
    "endpoint_configured": false,
    "llm_available": false,
    "timeout_seconds": 20
  },
  "execution_quality": "no_real_backend",
  "degraded_local_execution": true,
  "retry_recommended": true,
  "artifacts": []
}
```

If the result is not yet available, clients should keep polling `get_job_status` and use `progress.phase`, `progress.percent`, and `backend_state` to decide whether to wait, cancel, or submit a fallback remote task.

Result release semantics:

- `fetch_result` returns the broker-filtered release view, not the raw worker-emitted result blob
- sensitive jobs may have path-like fields redacted to `[REDACTED]`
- artifacts may be withheld entirely by default for `restricted`, `phi`, `secret_adjacent`, or `local_only` jobs
- an explicit per-job override such as `task_params.allow_artifact_release=true` may allow artifact metadata release, but artifact storage paths should still be removed
- clients should rely on schema fields such as `summary`, `top_findings`, `suggested_next_steps`, and `warnings` rather than expecting raw evidence inline

### `cancel_job`

Cancel a running or queued job.

Request:

```json
{
  "job_id": "job_01jz4f8j6q0j4w3p7gx4h0n1jv"
}
```

Response:

```json
{
  "job_id": "job_01jz4f8j6q0j4w3p7gx4h0n1jv",
  "state": "cancelled"
}
```

### `fetch_job_logs`

Fetch redacted worker logs for debugging without exposing raw cluster file paths.

Request:

```json
{
  "job_id": "job_01jz4f8j6q0j4w3p7gx4h0n1jv",
  "stream": "combined",
  "max_bytes": 8192
}
```

Response:

```json
{
  "job_id": "job_01jz4f8j6q0j4w3p7gx4h0n1jv",
  "state": "failed",
  "stream": "combined",
  "content": "== stdout ==\nBroker worker started\n\n== stderr ==\nfatal error: ...",
  "truncated": false,
  "max_bytes": 8192,
  "source_refs": ["stdout.log", "stderr.log"]
}
```

Log retrieval semantics:

- secrets matching simple bearer token and API key patterns should be redacted before response
- responses should be truncated to a caller-provided or broker-default byte budget
- broker should return logical source names like `stdout.log`, not absolute node-local paths
- this tool is for debugging and should not be used as a bulk artifact export channel
- jobs with `restricted`, `phi`, or `secret_adjacent` inputs should deny log release by default
- an explicit per-job override such as `task_params.allow_log_release=true` should be required for sensitive log return

### `list_local_capabilities`

Return the currently implemented local task, schema, backend, and cache capabilities.

Request:

```json
{}
```

Response:

```json
{
  "task_types": [
    {
      "name": "document_summary",
      "schema": "document_summary_v1",
      "inputs": ["file"]
    },
    {
      "name": "log_analysis",
      "schema": "log_analysis_v1",
      "inputs": ["file"]
    },
    {
      "name": "repo_summary",
      "schema": "repo_summary_v1",
      "inputs": ["directory", "repo"]
    }
  ],
  "backends": [
    {
      "name": "slurm",
      "modes": ["stub", "command"],
      "default_mode": "stub"
    }
  ]
}
```

## Optional Second-Wave MCP Tools

These should be added only after the core lifecycle is stable.

### `estimate_local_job`

Returns an estimate for runtime, likely queueing, resource class, and cacheability before submission.

### `list_cached_results`

Allows clients to understand what reusable local analysis already exists.

## MCP Input Model

The broker should accept the following core input concepts.

### Task Type

Examples:

- `repo_summary`
- `code_search`
- `static_analysis`
- `log_analysis`
- `test_failure_analysis`
- `root_cause_analysis`
- `patch_generation`
- `embedding_generation`
- `document_summary`

### Input References

Inputs should be passed by reference, not inlined, whenever possible.

Reference types:

- `repo`
- `directory`
- `file`
- `artifact`
- `log`
- `text`

Each input ref should support:

- `uri`
- `content_hash`
- `size_bytes`
- `classification`

### Constraints

Constraints are contractually important. They are not hints.

Fields:

- `max_input_tokens`
- `max_output_tokens`
- `max_runtime_seconds`
- `priority`
- `confidentiality`
- `allow_remote_escalation`

### Execution Profile

Execution profiles should remain logical and portable.

Fields:

- `backend`
- `model`
- `accelerator`
- `qos`
- `container_image`

## Broker Internal HTTP API

The broker should expose an internal API that is separate from MCP transport.

Suggested endpoints:

- `POST /v1/jobs`
- `GET /v1/jobs/{job_id}`
- `POST /v1/jobs/{job_id}:cancel`
- `GET /v1/jobs/{job_id}/result`
- `GET /v1/jobs/{job_id}/artifacts`
- `GET /v1/capabilities`
- `POST /v1/cache/lookup`
- `POST /v1/policy/evaluate`

This separation is important because:

- MCP tooling can evolve independently from internal service composition
- non-MCP clients may still need access
- testing becomes easier
- backend orchestration can be reused outside a specific agent ecosystem

## Result Contract

Results should be schema-first and compact.

Every result should include:

- `schema_name`
- `schema_version`
- `payload`
- `evidence_refs`
- `provenance`
- `quality_signals`

Whenever possible, the payload should avoid:

- dumping raw input
- large prose responses
- opaque blobs without evidence references

## Error Contract

Errors should be machine-readable.

Example:

```json
{
  "error": {
    "code": "PREEMPTED",
    "message": "Slurm job was preempted before completion.",
    "retryable": true,
    "details": {
      "backend": "slurm",
      "backend_job_id": "8123456"
    }
  }
}
```

Recommended error codes:

- `INVALID_REQUEST`
- `POLICY_DENIED`
- `CACHE_CORRUPT`
- `QUEUE_TIMEOUT`
- `PREEMPTED`
- `WORKER_TIMEOUT`
- `WORKER_OOM`
- `BACKEND_UNAVAILABLE`
- `RESULT_SCHEMA_INVALID`
- `NOT_FOUND`

## Compatibility Goal

The MCP boundary should be usable by:

- GitHub Copilot CLI
- Claude Code
- Codex CLI
- custom MCP-capable orchestrators

That is best achieved by keeping the tool surface minimal and keeping task-specific behavior inside job schemas and execution templates rather than multiplying tool names.
