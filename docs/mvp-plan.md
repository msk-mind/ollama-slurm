# MVP Plan

## Purpose

This document translates the architecture into the smallest credible implementation plan for this repository.

The objective is not to build the full long-term system immediately. The objective is to prove the core product claim:

- an MCP-capable remote agent can delegate token-intensive work
- the work runs locally as ordinary Slurm jobs
- sensitive raw data stays local by default
- the agent receives compact structured results

## MVP Product Boundary

The MVP should include:

- one broker service
- one execution backend: Slurm
- one metadata store
- one artifact store abstraction
- one policy mode: local-first with safe-summary release
- two or three worker task types
- four MCP tools:
  - `submit_local_job`
  - `get_job_status`
  - `fetch_result`
  - `cancel_job`

The MVP should not yet include:

- multiple backends
- advanced multi-tenant controls
- distributed task graphs
- broad UI surface
- generalized plugin ecosystem
- high-count task catalog

## MVP Success Criteria

The MVP is successful if it can demonstrate all of the following:

1. A remote agent submits a local job through MCP.
2. The broker schedules it as a normal Slurm job.
3. The worker analyzes a local input without sending raw content remotely.
4. The worker returns schema-validated JSON.
5. The broker stores and returns the result.
6. Cache hits work for repeated identical requests.
7. Basic policy checks prevent raw output leakage by default.

## Recommended MVP Task Set

Do not start with the hardest tasks first.

Recommended initial tasks:

### 1. `document_summary`

Why first:

- simple input model
- easy to validate structured output
- useful for proving the privacy boundary

### 2. `log_analysis`

Why second:

- directly aligned with expensive token-heavy debugging workflows
- easy to show value in compact structured output
- naturally demonstrates redaction and evidence refs

### 3. `repo_summary`

Why third:

- high strategic value
- begins to exercise repo hashing, chunk manifests, and larger staged input handling

Do not start MVP with:

- `patch_generation`
- `root_cause_analysis`
- `embedding_generation`

Those are valuable, but they add too many moving parts before the core lifecycle is proven.

## MVP Technical Choices

Recommended defaults for the first implementation:

- broker language: Go
- worker language: Python
- metadata store: PostgreSQL
- artifact store: filesystem abstraction first, S3-compatible interface shape
- backend: Slurm only
- runtime: start with tool-first or hybrid workers; add local model invocation where it clearly helps
- schemas: JSON Schema

Reasoning:

- Go is a good fit for an MCP server and backend control plane
- Python is practical for worker pipelines and model integration
- filesystem artifact storage reduces early deployment complexity while preserving the abstraction boundary

## MVP Directory Creation Plan

The first structural changes should create the future center of gravity without moving everything immediately.

### Step 1: Create control-plane directories

Create:

- `broker/cmd/broker-server/`
- `broker/pkg/mcp/`
- `broker/pkg/api/`
- `broker/pkg/backends/slurm/`
- `broker/pkg/store/`
- `broker/pkg/cache/`
- `broker/pkg/policy/`
- `broker/pkg/schemas/`

### Step 2: Create initial worker directories

Create:

- `workers/document-summary/`
- `workers/log-analysis/`
- `workers/repo-summary/`

### Step 3: Create deployment and config skeletons

Create:

- `deploy/slurm/`
- `configs/broker/`
- `configs/models/`
- `configs/policies/`

### Step 4: Add tests layout

Create:

- `tests/unit/`
- `tests/integration/`
- `tests/e2e/`

These directories should exist before major implementation so the repo signals the intended architecture clearly.

## MVP Execution Sequence

### Phase 1: Control Plane Skeleton

Build:

- broker config loading
- MCP server shell
- internal job model
- DB schema for jobs and results
- filesystem artifact abstraction

Deliverable:

- broker can accept a request and persist a job record, even before real execution

### Phase 2: Slurm Backend Skeleton

Build:

- Slurm backend interface implementation
- job submission path
- status polling
- cancellation path
- backend-to-broker state mapping

Deliverable:

- broker can submit a placeholder worker job and track lifecycle

### Phase 3: Worker Contract And One Task

Build:

- worker execution bundle format
- result schema validation
- one real worker, preferably `document_summary`
- artifact manifest generation
- terminal result ingestion

Deliverable:

- end-to-end flow from MCP submission to structured result retrieval

### Phase 4: Policy And Cache Baseline

Build:

- pre-execution checks
- pre-release safe-summary filtering
- exact-match result cache
- input hashing and idempotency key handling

Deliverable:

- repeated identical jobs can return cached results safely

### Phase 5: Add Log Analysis

Build:

- `log_analysis` worker
- redacted evidence artifacts
- basic failure classification

Deliverable:

- broker demonstrates a real debugging workflow with strong token-savings story

### Phase 6: Add Repo Summary

Build:

- repo manifest hashing
- chunk manifest generation
- `repo_summary` worker

Deliverable:

- broker demonstrates large local codebase summarization

## First End-To-End Demo

The first demo should be intentionally small and convincing.

Suggested scenario:

1. A local build log is staged.
2. A remote MCP agent calls `submit_local_job(task_type=log_analysis)`.
3. Broker submits a Slurm job.
4. Worker parses and summarizes the log.
5. Broker returns:
   - short summary
   - ranked findings
   - suggested next steps
   - evidence refs
6. A second identical request hits cache.

Why this is the right first demo:

- it is concrete
- it matches the product thesis
- it highlights privacy, scheduling, and token reduction simultaneously

## Suggested MVP Result Schemas

Start with only these:

- `document_summary_v1`
- `log_analysis_v1`
- `repo_summary_v1`

Avoid inventing many schemas before the result ingestion and validation flow is stable.

## MVP Policy Scope

Do not attempt full enterprise policy in the first cut.

Implement only:

- default local-only processing for non-public inputs
- safe summary release mode
- deny raw inline excerpts for restricted data
- approval-required path stub for future expansion

This is enough to establish the control point without overbuilding.

## MVP Cache Scope

Implement only:

- exact final-result cache
- content hashing for files and documents
- simple repo manifest hashing

Optional if cheap:

- parsed-log intermediate cache

Do not start with:

- semantic cache matching
- cross-task intermediate reuse across many task types
- cross-tenant cache sharing

## Testing Plan

### Unit Tests

Cover:

- schema validation
- input hashing
- policy decision logic
- Slurm state mapping
- cache key derivation

### Integration Tests

Cover:

- broker DB interactions
- result ingestion
- artifact manifest validation
- worker contract behavior

### End-To-End Tests

Cover:

- submit -> run -> fetch result
- submit -> cancel
- cache hit on repeated request
- restricted output redaction behavior

The MVP should not claim production readiness without at least one real e2e path.

## Migration Plan For Existing Assets

Do not rewrite the whole repo on day one.

Use the current assets as scaffolding:

- treat current Slurm scripts as references for the new Slurm backend
- treat current model configs as seed data for `configs/models/`
- preserve registry and connection helpers as legacy examples until the broker flow exists

The new code should establish the real product path first. Cleanup can follow.

## Risks To Manage During MVP

### Risk 1: Overbuilding The Task Layer

Mitigation:

- keep task count low
- prove lifecycle first

### Risk 2: Coupling Too Hard To Slurm Scripts

Mitigation:

- implement a backend interface early
- use the scripts as references, not as the permanent API

### Risk 3: Underbuilding Policy

Mitigation:

- implement a real pre-release control path even if rule set is small

### Risk 4: Premature Model Complexity

Mitigation:

- start with tool-first workers where possible
- only introduce local LLM calls where they add clear value

## Exit Criteria For MVP Completion

Call the MVP complete when:

- the broker exposes the four core MCP tools
- one agent can complete a full Slurm-backed local delegation workflow
- at least two task types work end-to-end
- schema validation, cache hits, and safe-summary policy are functional
- the design no longer depends on root-level legacy scripts for its primary workflow

## Recommended Immediate Next Step

Once this plan is accepted, the next implementation step should be to scaffold:

- `broker/`
- `workers/`
- `configs/`
- `deploy/slurm/`
- `tests/`

Then define the minimal DB schema and MCP handler skeleton before touching worker logic.

