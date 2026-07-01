# Local AI Compute Broker Architecture

## Purpose

This project should evolve from "run a local LLM server on Slurm" into an open-source `local AI compute broker` that allows remote frontier models to delegate token-intensive work to on-premise compute.

The broker is not intended to replace remote models. It is intended to:

- reduce remote token consumption
- keep sensitive data local by default
- let remote agents orchestrate local analysis
- reuse existing cluster infrastructure such as Slurm and OCI containers

The default operating model is:

1. A remote orchestrator model decides that a task is expensive or privacy-sensitive.
2. It calls MCP tools exposed by the broker.
3. The broker schedules local work on existing compute infrastructure.
4. Local workers analyze large inputs and return structured JSON, evidence, and optional candidate fixes.
5. The remote model receives only compact outputs unless the user explicitly authorizes raw data disclosure.

## Design Principles

- `Remote orchestration, local execution`: frontier models remain the orchestrator; on-premise systems do the heavy reading and analysis.
- `RAG compression before remote synthesis`: local workers should return evidence-preserving compressed evidence packs, not raw data or generic summaries.
- `Structured outputs over prose`: workers should emit JSON schemas whenever possible.
- `Local-first privacy`: raw repositories, logs, documents, PHI, and proprietary data stay local unless explicitly exported.
- `Standard interfaces`: prefer MCP, OpenAI-compatible inference APIs, Slurm, OCI containers, JSON Schema, and S3-compatible object stores.
- `No permanently reserved GPUs`: all compute runs as ordinary scheduled jobs.
- `Cache aggressively`: reuse expensive intermediate work using content-addressed hashes.
- `Backend portability`: Slurm is first, not special. The broker should be able to route to Kubernetes, Ray, or standalone GPU hosts later.

## System Context

```text
Developer
  |
  v
GitHub Copilot CLI / Claude Code / Codex CLI
  |
  v
MCP Server: Local AI Compute Broker
  |
  +--> Policy Engine
  +--> Router / Planner
  +--> Cache / Artifact Store
  +--> Backend Adapters
          |
          +--> Slurm
          +--> Kubernetes
          +--> Ray
          +--> Standalone GPU servers
                    |
                    v
              Worker Runtime
                    |
                    +--> llama.cpp
                    +--> vLLM
                    +--> SGLang
                    +--> tree-sitter / rg / static analyzers
                    +--> embedding models / summarizers / patch generators
```

The primary northbound product is therefore:

```text
Remote LLM orchestrator
  -> MCP Broker
  -> local discovery, chunking, retrieval, reranking, deduplication, compression
  -> validated compressed evidence pack
  -> remote final synthesis
```

Raw local inputs should not be sent back to the remote model by default.

## Major Components

### 1. MCP Broker

Northbound interface for MCP-capable agents.

Responsibilities:

- expose stable MCP tools
- validate and normalize requests
- enforce policy and token budgets
- route requests to execution backends
- provide job status and result retrieval
- return structured summaries and artifact references

### 2. Policy Engine

Decision point for confidentiality and export control.

Responsibilities:

- classify inputs and outputs
- determine whether raw data can leave the local environment
- enforce path-, repo-, user-, and model-level rules
- require explicit override for sensitive data export
- redact or block disallowed outputs

### 3. Router / Planner

Task-to-execution translator.

Responsibilities:

- map `task_type` to execution templates
- choose local models and runtime backends
- prefer cheaper non-LLM tools first when possible
- choose interactive vs background execution profiles
- compute expected resource requirements
- choose retrieval, reranking, compression, and GPU tier for RAG compression tasks

### 4. RAG Compression Pipeline

Local evidence reduction subsystem.

Responsibilities:

- discover broker-authorized inputs
- chunk code, logs, documents, and git data with source references
- build local indexes for lexical, structural, semantic, stack-trace, and git-history retrieval
- retrieve, rerank, and deduplicate candidate chunks
- compress evidence while preserving paths, line ranges, timestamps, commit hashes, artifact IDs, and content hashes
- validate final evidence packs against schema and token budgets
- keep raw repositories, logs, documents, PHI, and proprietary data local by default

Supported retrieval strategies:

- ripgrep and BM25 for logs, identifiers, exact errors, and code
- tree-sitter-aware chunking and symbol retrieval for code
- embeddings for documents and semantic queries
- stack-trace and path-aware retrieval for debugging
- git diff and history-aware retrieval for regressions and patch review

### 5. Backend Adapter Layer

Execution abstraction that hides scheduler-specific details.

Responsibilities:

- submit jobs
- poll status
- cancel jobs
- fetch logs and terminal metadata
- reconcile scheduler state with broker state

Initial implementation:

- `SlurmBackend`

Planned implementations:

- `KubernetesBackend`
- `RayBackend`
- `StandaloneGpuBackend`

### 6. Worker Runtime

Execution environment for local analysis tasks.

Responsibilities:

- read immutable job spec
- mount or fetch staged inputs
- run analysis, inference, or indexing tools
- emit structured result JSON
- persist artifacts and metrics
- send heartbeats and progress updates

Workers may run:

- `llama.cpp`
- `vLLM`
- `SGLang`
- code search and parsing tools
- embedding models
- test/log/static-analysis pipelines

Routine RAG compression should prefer low-contention P40 GPUs when model inference is needed. A100 GPUs should be reserved for hard reasoning, large-context compression, patch generation, or other high-memory work.

### 7. Metadata Store

Control-plane source of truth.

Responsibilities:

- store jobs and execution plans
- record policy decisions
- maintain cache indexes
- store model capability metadata
- record audit events

Suggested default:

- PostgreSQL

### 8. Artifact Store

Blob storage for inputs, outputs, and intermediates.

Responsibilities:

- store logs, summaries, embeddings, patches, manifests
- store structured result blobs
- store intermediate parse/index outputs
- support retention rules and access control

Suggested default:

- S3-compatible object storage
- local filesystem abstraction for development

## Execution Modes

### Interactive

Used for debugging, triage, and short investigations.

Examples:

- summarize a failing test log
- search a repo for all callers of a symbol
- extract top hypotheses from recent build output

Properties:

- lower latency target
- smaller scopes
- stronger preference for cache reuse
- usually preemptible but short-lived

### Background

Used for broad investigations and long-running analysis.

Examples:

- summarize a large repository
- batch-generate embeddings
- run root-cause analysis across many logs
- produce candidate fixes for a complex failure

Properties:

- longer runtime
- broader scope
- stronger batching support
- designed for retries and preemption

## Task Classes

The first release should prioritize tasks that are expensive in tokens and naturally local:

- `repo_summary`
- `code_search`
- `static_analysis`
- `log_analysis`
- `test_failure_analysis`
- `root_cause_analysis`
- `document_summary`
- `embedding_generation`
- `batch_extraction`
- `patch_generation`
- `rag_compression`

The broker should remain model-agnostic and task-agnostic by using schemas, execution templates, and backend adapters rather than hard-coded model flows.

## Data Flow

1. MCP client submits a job request.
2. Broker normalizes the request into a canonical job spec.
3. Policy engine evaluates access rules and export constraints.
4. Cache layer checks for exact, retrieval, index, chunk, embedding, or evidence-pack reuse.
5. Router selects execution profile, retrieval strategy, compression model, backend, and container.
6. Broker stages immutable inputs and submits the job.
7. Scheduler launches a worker.
8. Worker performs input discovery, chunking, local indexing, retrieval, reranking, deduplication, and evidence-preserving compression.
9. Worker emits a validated structured evidence pack or task-specific JSON result backed by evidence references.
10. Broker validates outputs against schema, token budgets, and pre-release policy.
11. Broker stores artifacts, updates cache indexes, and exposes only allowed compressed results.
12. Remote orchestrator performs final synthesis from the evidence pack.

## RAG Compression Budgets

The broker should enforce token budgets throughout the RAG pipeline:

- retrieved chunk budget
- per-chunk compression budget
- final evidence-pack budget
- remote model context budget

Budget overruns should fail validation or trigger deterministic trimming before the remote model sees the result.

## GPU Tiering

Default scheduling tiers:

- `cpu-rag-indexing`: discovery, hashing, chunking, ripgrep, BM25, tree-sitter parsing, and deduplication
- `p40-rag-compression`: default low-contention tier for embeddings, reranking, and small local compression passes that fit 24 GB VRAM
- `a100-reasoning`: hard reasoning, large-context compression, patch generation, and larger local models

All tiers should run as ordinary scheduler jobs. GPUs should not be permanently reserved for the broker.

## Relationship To Current Repository

This repository already contains useful first-generation building blocks:

- Slurm submission scripts
- model configuration files
- a lightweight registry for launched local model servers
- operational documentation for Claude CLI and llama.cpp

Those assets should be retained, but repositioned as:

- the first execution backend bootstrap
- model runtime templates
- operational examples

They should not remain the primary architectural boundary of the project. The architectural center should become the broker control plane and its job/result contracts.

## Recommended Initial Boundary

The first product boundary should be:

- one MCP server
- one backend adapter: Slurm
- one artifact store abstraction
- one metadata DB
- a small number of worker task types
- schema-first JSON outputs

That is sufficient to prove the model:

- remote agents orchestrate work
- local cluster executes expensive analysis
- raw sensitive inputs stay local
- remote token usage drops materially
