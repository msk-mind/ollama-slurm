# Operations

## Purpose

This document describes how the broker should be deployed, observed, and recovered in production-like environments.

The goal is not to lock the project into one topology. The goal is to define a practical operational baseline that works well for the first Slurm-backed deployment and still fits the longer-term multi-backend architecture.

## Operational Goals

- `reliable`: broker restarts should not orphan all knowledge of running jobs
- `secure`: sensitive inputs and outputs remain controlled in storage and transit
- `observable`: operators can understand job flow, backend health, and failure causes
- `recoverable`: common failure modes have clear reconciliation paths
- `incremental`: first deployment should not require a large platform build-out

## Minimum Production Components

The first serious deployment should include:

- broker service
- metadata database
- artifact storage
- one backend adapter, initially Slurm
- worker runtime images or environments
- observability stack
- policy configuration bundle

## Suggested First Deployment Topology

```text
Developer CLI / MCP Agent
        |
        v
   Broker Service
        |
        +--> PostgreSQL
        +--> Artifact Store
        +--> Policy Bundle
        +--> Metrics / Logs / Traces
        |
        v
    Slurm Backend
        |
        v
   Worker Jobs On Cluster Nodes
        |
        +--> Local model runtimes
        +--> Tooling runtimes
        +--> Artifact upload / shared output path
```

## Broker Service Deployment

The broker service should be deployable in at least two ways:

- `systemd-managed host service`
- `containerized service`

For the first Slurm-centric environment, a host service on a login or control node may be the most practical starting point if cluster policy allows it.

Recommended properties:

- stateless application process
- persistent state only in DB and artifact store
- configurable through environment variables and config files
- health and readiness endpoints
- structured JSON logs

## Metadata Database

Recommended default:

- PostgreSQL

Responsibilities:

- jobs
- execution plans
- backend run tracking
- cache index
- policy decisions
- audit events

Operational guidance:

- enable regular backups
- use migrations for schema evolution
- keep application and migration versions visible in release notes

## Artifact Storage

Recommended default:

- S3-compatible object store

Development fallback:

- local filesystem abstraction

Responsibilities:

- result blobs
- artifact manifests
- redacted excerpts
- patch diffs
- embedding indexes
- intermediate reusable outputs

Operational guidance:

- enforce retention rules by artifact type and classification
- validate content hashes on read and write
- encrypt at rest if environment requires it

## Slurm Integration Model

For the first deployment, Slurm remains the execution backbone.

Operational expectations:

- jobs are submitted as ordinary batch jobs
- GPUs are allocated only while work is running
- low-priority or preemptible QoS is the default
- worker images or environments are available on compute nodes
- scheduler logs and output paths are discoverable for reconciliation

Recommended operational profiles:

- `interactive-light`
- `interactive-heavy`
- `background-batch`
- `indexing`

These profiles should map to default Slurm resources, QoS, timeouts, and model/runtime choices.

## Worker Runtime Deployment

Workers should be reproducible and versioned.

Recommended options:

- OCI containers if supported by cluster tooling
- otherwise versioned virtual environments or module-based runtime bundles

Operational requirements:

- model runtime dependencies must be known and versioned
- workers must be able to access staged inputs
- workers must be able to emit artifacts and heartbeats
- workers must not depend on ad hoc mutable node state

## Configuration Management

Configurations should be externalized.

Examples:

- broker service config
- model profiles
- runtime endpoint profiles
- backend profiles
- routing rules
- policy bundles
- retention settings

Recommended formats:

- YAML
- TOML
- JSON for generated machine-level configs

Configuration changes that alter semantics should be versioned and referenced in job provenance where relevant.

Current implementation notes:

- broker config can now stage runtime connection metadata into `execution_plan.json`
- this includes runtime-specific `base_url` and timeout fields for `llama.cpp`, `vLLM`, and `SGLang`
- workers should prefer staged runtime connection metadata over ad hoc environment discovery

## Authentication And Access Control

Operational baseline:

- authenticate callers to the broker API
- authorize job submission, fetch, and cancel actions
- scope artifact access to user, project, or tenant

Possible deployment patterns:

- internal token-based auth
- mTLS between trusted services
- SSO-backed service gateway in larger environments

The first version does not need to solve enterprise identity for every environment, but it does need a clean auth boundary.

Current implementation notes:

- `BROKER_AUTH_MODE=header` preserves header-derived identity via `X-Broker-Actor` and `X-Broker-Role`
- `BROKER_AUTH_MODE=static_tokens` requires `Authorization: Bearer <token>` and maps tokens from `BROKER_STATIC_TOKENS`
- `BROKER_STATIC_TOKENS` uses `token=actor:role` entries separated by commas
- header identity mode should only be used behind an authenticated internal gateway

## Observability

The broker should expose enough telemetry to answer:

- are jobs being accepted?
- where are they spending time?
- are cache hits reducing work?
- are workers failing due to model issues, scheduler issues, or policy?
- are restricted artifacts being handled correctly?

### Metrics

Recommended metrics:

- job submissions by task type
- job terminal states
- queue latency
- run latency
- cache hit rate
- partial reuse rate
- backend submission failures
- worker heartbeat timeouts
- artifact upload failures
- policy denies and approval-required counts

### Logs

Recommended logging behavior:

- structured JSON logs
- job ID and backend run ID on all relevant events
- append-only audit events written separately from ordinary service logs
- per-run worker stdout and stderr copied into the staged run directory as `stdout.log` and `stderr.log`
- log retrieval APIs should apply redaction and byte truncation before returning content to MCP clients
- avoid raw sensitive payloads in logs
- separate audit logs from ordinary operational logs when possible

Current implementation notes:

- broker service and MCP service write JSONL audit events to `BROKER_AUDIT_LOG_PATH`
- default path: `.broker/audit.jsonl`
- startup verification policy is controlled by `BROKER_AUDIT_VERIFY_MODE`
- broker background maintenance can auto-rotate when `BROKER_AUDIT_ROTATE_BYTES` is exceeded
- rotated retention is controlled by `BROKER_AUDIT_KEEP_ARCHIVES`
- maintenance cadence is controlled by `BROKER_AUDIT_MAINTAIN_INTERVAL_SECONDS`
- `GET /v1/system/audit-health` provides an admin-only live integrity check for the active audit chain
- the audit health endpoint returns `200` for a valid chain, `503` for a broken chain, and `501` when no audit sink is configured
- current actions include submit, status fetch, list, result fetch, log fetch, and cancel
- audit events carry actor, role, action, outcome, job ID, and selected safe metadata
- records are hash-chained with `prev_hash` and `event_hash` so tampering is locally detectable
- `broker-cli verify-audit --path .broker/audit.jsonl` validates the current chain and exits non-zero on failure
- `broker-cli rotate-audit --path .broker/audit.jsonl` rotates the active file and preserves chain continuity via a seed-hash sidecar
- `broker-cli prune-audit --path .broker/audit.jsonl --keep 10` removes older rotated segments while preserving the newest retained archives

### Traces

If feasible, use distributed tracing for:

- MCP request handling
- policy evaluation
- cache lookup
- backend submission
- artifact ingestion

OpenTelemetry is a reasonable default.

## Audit Operations

Operators need both startup-time and runtime audit integrity checks.

Recommended operating pattern:

- keep `BROKER_AUDIT_VERIFY_MODE=fail` in production-like environments so the service will not start on a broken active chain
- use `GET /v1/system/audit-health` for live admin verification after startup and after any storage or maintenance event
- use `broker-cli verify-audit` during incident response or before archival export
- use `broker-cli rotate-audit` and `broker-cli prune-audit` only as controlled maintenance operations unless automatic maintenance is enabled

Expected responses from `GET /v1/system/audit-health`:

- `200` when the active audit file validates
- `503` when validation finds a gap or tamper condition
- `501` when the broker is running without an audit file configured
- `403` for non-admin callers

## Alerting

The first alert set should stay small and high-signal.

Examples:

- broker unavailable
- DB unavailable
- artifact store unavailable
- sustained job submission failures
- large increase in worker heartbeat timeouts
- reconciliation backlog growing
- repeated policy engine failures

Avoid noisy alerts on every individual failed job.

## Reconciliation And Recovery

The broker should assume restarts and partial failures will happen.

Recovery mechanisms should include:

- periodic reconciliation loop
- durable storage of backend run IDs
- ability to rebuild state from DB plus backend status
- ability to re-ingest artifacts from terminal runs

### Broker Restart Scenario

On startup, the broker should:

1. restore unfinished jobs from DB
2. poll backend state for associated runs
3. reconcile missing or changed terminal states
4. resume polling and artifact collection

### Lost Worker Heartbeat Scenario

The broker should:

1. detect missed heartbeat threshold
2. poll backend for run state
3. inspect logs if available
4. mark failed, preempted, or unknown based on evidence

### Artifact Ingestion Failure Scenario

The broker should:

1. preserve job terminal metadata
2. retry artifact collection when safe
3. quarantine partial results if hashes or manifests do not validate

## Backups And Retention

Back up:

- PostgreSQL
- policy bundles
- critical broker configuration

Artifact retention should depend on artifact type and sensitivity.

Examples:

- short retention for raw logs
- medium retention for redacted summaries
- longer retention for reusable indexes when policy permits

The first production deployment should document concrete retention defaults.

## Release Management

Recommended practices:

- semantic versioning for broker service
- explicit schema versions
- migration notes for DB and config changes
- compatibility notes for workers and backends

Every release should identify:

- changed schemas
- changed cache semantics
- changed policy behavior
- required migrations

## Environment Tiers

The project should support at least three operating tiers.

### Development

- local filesystem artifact store
- single broker instance
- mock or test backend

### Staging

- real PostgreSQL
- real artifact store
- limited Slurm integration
- realistic policies and schemas

### Production

- authenticated broker
- monitored DB and artifact store
- backup and restore plan
- audit logging
- controlled release process

## Operational Readiness Checklist

Before calling the system production-ready, verify:

- broker restart does not lose active job tracking
- queue, run, and artifact failures are visible
- audit events are being recorded
- restricted outputs remain local by default
- cache behavior is observable and correct
- policy changes can be rolled out safely
- Slurm integration works under preemption and timeout conditions
