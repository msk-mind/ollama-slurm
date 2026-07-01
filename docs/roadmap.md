# Roadmap

## Goal

Build an open-source local AI compute broker that allows remote MCP-capable agents to delegate expensive work to on-premise compute without exposing raw sensitive inputs by default.

The roadmap below is ordered to validate the architecture before broadening backend support or task count.

## Current Snapshot

As of July 1, 2026, the repository already contains a partial implementation of the control plane:

- broker HTTP API for job submission, status, list, result fetch, log fetch, and cancel
- stdio MCP server with `submit_local_job`, `get_job_status`, `fetch_result`, `fetch_job_logs`, `cancel_job`, and `list_local_capabilities`
- Slurm backend adapter
- file-backed cache and metadata store
- structured worker result validation
- heartbeat-based progress reporting
- authentication, per-job authorization, and filtered release views for sensitive outputs
- tamper-evident audit logging with verification, rotation, pruning, and admin health checks
- design docs for evidence-preserving RAG compression as the primary remote-token reduction mechanism

The milestones below should therefore be read as a mix of completed baseline, hardening work, and expansion work.

## Milestone 0: Design Baseline

Status:

- largely complete

Objective:

- define the product boundary before implementation

Deliverables:

- architecture spec
- MCP tool spec
- broker API shape
- data model
- security model
- scheduler abstraction
- initial threat model

Exit criteria:

- task taxonomy is stable enough to implement
- control-plane responsibilities are clearly separated from worker logic
- Slurm is framed as a backend, not the product itself

## Milestone 1: Slurm Broker MVP

Status:

- partially complete

Objective:

- prove remote orchestration with local scheduled execution

Scope:

- one MCP server
- one backend adapter: Slurm
- one metadata store
- one artifact store abstraction
- a small number of task types
- schema-first structured outputs

Suggested first task types:

- `log_analysis`
- `document_summary`
- `repo_summary`

Deliverables:

- `submit_local_job`
- `get_job_status`
- `fetch_result`
- `cancel_job`
- `fetch_job_logs`
- `list_local_capabilities`
- Slurm job submission and status reconciliation
- basic content-hash cache
- local-only confidentiality mode

Exit criteria:

- agent can submit work and receive structured results
- jobs run as ordinary Slurm jobs
- no GPUs are permanently reserved
- cache hits are observable and correct

Remaining work:

- tighten artifact-store abstraction beyond local filesystem staging
- formalize deployment packaging and configuration bundles
- expand end-to-end coverage around policy and sensitive-result release paths

## Milestone 2: RAG Compression And Repo Intelligence

Status:

- not started beyond basic repo summary worker

Objective:

- make evidence-preserving RAG compression the default path for repository, log, and document analysis before any remote synthesis

Scope:

- input discovery and content hashing
- repo chunking and manifests
- tree-sitter-aware code chunking
- local indexing with ripgrep/BM25 for logs and code
- embeddings for documentation and mixed natural-language corpora
- stack-trace/path-aware retrieval
- git diff/history-aware retrieval
- retrieval, reranking, deduplication, and compression pipelines
- evidence-pack JSON validation
- P40 default execution tier for routine RAG compression
- code search
- symbol indexing
- static-analysis orchestration
- intermediate cache for parse, chunk, index, retrieval, rerank, and evidence-pack outputs

Deliverables:

- `rag_compress` MCP tool
- `debug_with_local_context` MCP tool
- `summarize_logs` MCP tool
- `inspect_repo` MCP tool
- `code_search` task
- `static_analysis` task
- `rag_evidence_pack_v1`
- `debug_evidence_pack_v1`
- `log_evidence_pack_v1`
- `repo_inspection_pack_v1`
- cache keys for files, chunks, embeddings, indexes, retrieval results, rerank results, evidence packs, and local model outputs
- broker endpoints for RAG compression, repo inspection, log summarization, and cache lookup

Exit criteria:

- large repo analysis avoids repeated raw rereads
- remote agent can navigate a repo using compact local evidence packs and search results
- raw repositories, logs, documents, PHI, and proprietary data stay local by default
- evidence packs preserve file paths, line ranges, timestamps, commit hashes, or equivalent references
- token budgets are enforced for retrieved chunks, per-chunk compression, final evidence packs, and remote context
- P40 jobs handle routine compression without reserving GPUs permanently

## Milestone 3: Failure Analysis And Candidate Fixes

Status:

- not started

Objective:

- support real debugging workflows that materially reduce remote token usage

Scope:

- test failure analysis
- build log RCA
- candidate patch generation from validated evidence packs
- patch packaging and validation hooks

Deliverables:

- `test_failure_analysis`
- `root_cause_analysis`
- `propose_patch`
- `patch_generation`
- `patch_proposal_pack_v1`
- confidence and provenance fields in result schemas

Exit criteria:

- agent can investigate a failure mostly through local RAG compression jobs
- patch proposals cite evidence IDs, paths, line ranges, and validation steps
- remote output remains compact, structured, and evidence-backed

## Milestone 4: Security And Production Hardening

Status:

- partially complete

Objective:

- make the broker suitable for shared or sensitive environments

Scope:

- authentication and authorization
- policy engine integration
- audit logs
- artifact retention policies
- output redaction pipeline
- observability and operational runbooks

Deliverables:

- policy enforcement hooks
- secure result retrieval rules
- tamper-evident or append-only audit design
- metrics, tracing, and alerting baseline

Exit criteria:

- restricted inputs stay local by default
- policy decisions are visible and testable
- operational failure modes are observable

Already present in the repository:

- caller authentication modes
- owner/admin authorization checks
- sensitive log and artifact release controls
- tamper-evident audit chain
- audit verification and maintenance tooling

Still missing for milestone completion:

- stronger artifact-retention enforcement
- production metrics and tracing exports
- deployment-grade runbooks and alerting
- external policy-engine integration if desired

## Milestone 5: Multi-Backend Execution

Objective:

- avoid coupling the project to a single scheduler

Scope:

- backend capability abstraction
- Kubernetes adapter
- standalone GPU server adapter
- optional Ray adapter

Deliverables:

- shared backend interface
- routing logic based on backend capabilities
- backend-specific integration tests

Exit criteria:

- identical broker job semantics across at least two backends
- task templates do not depend on Slurm-specific behavior

## Milestone 6: Ecosystem And Distribution

Objective:

- make the project usable by external contributors and operators

Scope:

- install and deployment documentation
- container publishing
- reference policies
- example MCP client integrations
- public JSON schemas

Deliverables:

- examples for Copilot CLI, Claude Code, and Codex CLI
- deployment artifacts for systemd and container-based environments
- contributor guide and architecture docs

Exit criteria:

- an external team can deploy the broker without reverse-engineering the repo

## Recommended Sequencing Decisions

- build the control plane before broad model coverage
- build strict output contracts before clever prompting
- build RAG compression before broad patch generation
- build cacheable intermediates before scaling task count
- build Slurm as a clean adapter before adding another scheduler
- build policy hooks before enabling wider artifact export

## Deferred Work

These are valuable, but should not block the first production architecture:

- distributed map/reduce-style task graphs
- automatic multi-worker decomposition
- advanced verifier models for patch scoring
- federated brokers across clusters
- per-team cost attribution and chargeback
- web UI for job exploration

## Success Criteria

The project is succeeding if:

- remote token usage for large local tasks drops substantially
- developers can keep sensitive corp data local by default
- local GPUs are used opportunistically instead of being reserved
- the broker becomes a general delegation layer for MCP-capable agents rather than a single-model launcher
