# Backend Interface

## Purpose

The broker should treat Slurm as the first execution backend, not the only one. That requires a stable backend interface that preserves broker-level semantics while allowing scheduler-specific implementations underneath.

This document defines:

- the responsibilities of a backend adapter
- the broker-to-backend contract
- the scheduler capability model
- Slurm-specific implications

## Design Goals

- `portable`: the broker should submit the same logical job to different backends
- `minimal`: keep the backend interface small
- `observable`: expose enough state for reliable reconciliation
- `retry-friendly`: support preemption and transient infrastructure failure
- `artifact-aware`: support staged inputs and collected outputs
- `policy-neutral`: policy decisions happen before backend submission
- `parallel-friendly`: support many runs under one logical investigation

## Non-Goals

The backend adapter should not:

- define MCP tool semantics
- perform authorization decisions
- define task schemas
- decide what output may be exported remotely
- embed task-specific prompt logic

## Core Abstraction

The broker should translate a `JobSpec` plus `ExecutionPlan` into a scheduler-specific `BackendRun`.

Conceptually:

```text
JobSpec + ExecutionPlan
        |
        v
Backend Adapter
        |
        v
BackendRun
```

The backend adapter owns the mapping between broker concepts and scheduler-native objects.

One logical investigation may map to many backend runs when the broker fans out shard or specialist jobs.

## Required Backend Operations

Every backend should implement these operations.

### `SubmitRun`

Inputs:

- immutable job spec reference
- execution plan
- staged input references
- output destination

Outputs:

- backend run ID
- accepted timestamp
- initial backend metadata

Responsibilities:

- translate resource shape into scheduler-specific submission format
- stage execution metadata required by the worker
- return a durable identifier for later reconciliation

### `GetRun`

Inputs:

- backend run ID

Outputs:

- scheduler-native state
- start/end timestamps if available
- assigned node or pod identity if available
- exit metadata if terminal

Responsibilities:

- provide authoritative backend status for reconciliation

### `CancelRun`

Inputs:

- backend run ID

Outputs:

- cancellation acknowledgement

Responsibilities:

- best-effort cancellation of queued or running work

### `ListRuns`

Inputs:

- optional filters such as owner, age, state, broker job ID

Outputs:

- set of backend runs

Responsibilities:

- support reconciliation sweeps
- support stale-job repair and drift detection

### `FetchRunLogs`

Inputs:

- backend run ID

Outputs:

- log references or log content

Responsibilities:

- allow debugging and failure analysis
- keep raw logs local by default

### `FetchRunArtifacts`

Inputs:

- backend run ID

Outputs:

- paths or references to worker-emitted artifacts

Responsibilities:

- connect scheduler execution to broker artifact ingestion

## Suggested Interface Shape

Illustrative pseudocode:

```text
type Backend interface {
  Name() string
  Capabilities() BackendCapabilities
  SubmitRun(ctx, SubmitRequest) (SubmitResponse, error)
  GetRun(ctx, backendRunID) (RunStatus, error)
  CancelRun(ctx, backendRunID) (CancelResult, error)
  ListRuns(ctx, filters) ([]RunStatus, error)
  FetchRunLogs(ctx, backendRunID) (LogRef, error)
  FetchRunArtifacts(ctx, backendRunID) ([]ArtifactRef, error)
}
```

The exact programming language is not important here. The interface boundary is.

## Backend Capability Model

The broker should not hard-code assumptions about what a backend can do.

Suggested capabilities:

```json
{
  "name": "slurm",
  "supports_gpu": true,
  "supports_preemption": true,
  "supports_arrays": true,
  "supports_dependencies": true,
  "supports_containers": true,
  "supports_priority_classes": true,
  "supports_interactive_latency_class": false,
  "supports_local_filesystem_mounts": true
}
```

Parallel-execution-relevant capabilities include:

- `supports_arrays`
- `supports_dependencies`
- `supports_partial_cancellation`
- `supports_shared_output_staging`

The router can use these capabilities to choose a backend without writing backend-specific logic into task planners.

## Broker-Level State Mapping

The backend should report scheduler-native states, but the broker must map them into stable public job states.

Recommended public states:

- `queued`
- `dispatching`
- `running`
- `succeeded`
- `failed`
- `cancelled`
- `timed_out`
- `preempted`

Examples:

- Slurm `PENDING` -> `queued`
- Slurm `RUNNING` -> `running`
- Slurm `COMPLETED` -> `succeeded`
- Slurm `CANCELLED` -> `cancelled`
- Slurm `PREEMPTED` -> `preempted`
- Slurm `TIMEOUT` -> `timed_out`
- Slurm `OUT_OF_MEMORY` or equivalent signal -> `failed` with `WORKER_OOM`

The mapping should live in the backend adapter, not in the MCP layer.

## Input And Output Staging

The broker should not assume all backends can directly see the same filesystem.

Inputs should therefore be staged via explicit references:

- local filesystem paths
- object store URIs
- mounted volumes
- generated manifests

Outputs should be written to a known location and ingested by the broker through:

- shared path pickup
- object-store upload
- explicit artifact manifest

That keeps the execution contract portable across backends.

## Reconciliation Model

The backend adapter should support both event-driven and polling-based reconciliation.

Minimum viable model:

- broker stores the submitted backend run ID
- broker periodically polls backend state
- worker heartbeats update broker progress
- terminal state triggers artifact collection and validation

A reconciler should also handle:

- broker restart during running jobs
- backend jobs that disappear unexpectedly
- worker-completed jobs that failed to post terminal status

## Slurm Backend

Slurm is the first implementation and should influence the interface only where genuinely necessary.

### Slurm Responsibilities

- render a job submission template or batch script
- attach broker job metadata to the Slurm job
- submit with low-priority or preemptible QoS by default
- capture Slurm job ID as backend run ID
- poll status via `squeue` and `sacct` or equivalent
- cancel via `scancel`
- retrieve stdout/stderr log paths

### Slurm Metadata To Preserve

- Slurm job ID
- partition
- QoS
- allocated node list
- exit code
- submit/start/end times
- preemption or timeout reason if present

### Slurm-Specific Risks

- queue latency may be large relative to interactive expectations
- stdout/stderr may be the only failure signal when workers crash early
- container behavior varies by site tooling
- staging can fail after job submission but before useful worker startup

The broker should treat those as adapter concerns, not user-visible protocol leaks.

## Other Backend Implications

### Kubernetes

Likely better for:

- lower startup latency
- container-native execution
- service-oriented runtimes

Potential tradeoff:

- weaker fit for environments where Slurm is the established shared scheduler

### Ray

Likely better for:

- distributed fan-out workloads
- fine-grained task graphs

Potential tradeoff:

- additional operational complexity for the first release

### Standalone GPU Servers

Likely better for:

- simple dedicated environments
- smaller deployments without a scheduler

Potential tradeoff:

- weaker fairness and queueing semantics

## Failure Semantics

The backend adapter should classify failures into broker-meaningful categories.

Recommended categories:

- submission failure
- queue timeout
- runtime timeout
- preemption
- infrastructure unavailable
- artifact collection failure
- scheduler state unknown

These categories should feed retry policy and user-visible error codes.

## Testing Strategy

Each backend adapter should have:

- unit tests for state mapping
- integration tests for submit, poll, cancel
- failure-path tests for missing logs, lost jobs, and timeouts

The broker should also run backend conformance tests to ensure two backends expose equivalent logical semantics.
