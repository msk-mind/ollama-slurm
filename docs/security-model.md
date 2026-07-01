# Security Model

## Security Objective

The system should allow remote AI agents to orchestrate local computation without silently leaking raw sensitive data to remote providers.

The default answer to "may this leave the local environment?" should be `no`, unless a policy decision or explicit user override says otherwise.

## Core Security Principles

- `local-first confidentiality`: raw code, logs, documents, and PHI remain local by default
- `least privilege`: workers only see the inputs needed for the task
- `explicit export`: raw content or broad excerpts require an explicit policy path
- `auditability`: high-trust actions are recorded
- `defense in depth`: transport, storage, execution, and output controls all matter

## Trust Boundaries

### 1. Developer Workstation / Agent

The orchestrator may be a frontier model accessed through a CLI agent. It is powerful but should not automatically receive local raw data.

### 2. Broker Control Plane

This is the main trust enforcement boundary.

The broker is responsible for:

- policy checks
- task normalization
- output filtering
- access control
- audit logging

### 3. Execution Plane

Workers and scheduler nodes are trusted to execute approved local tasks, but should not have broad unbounded access.

### 4. Storage Plane

Artifacts and metadata may contain sensitive material and require access control, encryption, and retention policies.

## Threat Model

Primary threats:

- unintentional disclosure of proprietary repo content to remote APIs
- leakage of PHI or restricted logs in generated summaries
- worker jobs with overly broad filesystem access
- persistence of sensitive artifacts longer than intended
- replay or reuse of cached results across tenants or projects
- forged or unauthorized MCP requests
- scheduler/job metadata drift causing incorrect job attribution

The first production version should document a concrete threat model with:

- assets
- actors
- trust assumptions
- abuse cases
- mitigations

## Data Classification

Inputs and outputs should carry classifications.

Minimum useful classes:

- `public`
- `internal`
- `restricted`
- `phi`
- `secret_adjacent`

These classifications should influence:

- export policy
- retention windows
- cache visibility
- allowed execution backends
- audit requirements

## Policy Enforcement

The broker should enforce policy before execution and before result release.

Example decisions:

- allow local-only processing of a restricted repo
- allow redacted summary export to remote orchestrator
- deny raw log excerpt export because logs contain PHI
- allow patch metadata export but not full file contents
- allow filtered `fetch_result` output while withholding artifact paths and raw evidence for sensitive jobs

Recommended policy inputs:

- user identity
- project or tenant
- input classifications
- requested task type
- target remote model or provider
- requested output schema
- explicit user overrides

Recommended outputs:

- `allow`
- `deny`
- `allow_with_redaction`
- `allow_with_approval`

## Output Security

Workers should never directly expose raw output to the orchestrator.

Instead:

1. Worker emits structured result and artifacts locally.
2. Broker validates schema and runs output policy checks.
3. Broker redacts, filters, or blocks fields as required.
4. MCP client receives only the allowed subset.

Sensitive outputs that deserve special handling:

- raw file excerpts
- large stack traces
- customer identifiers
- PHI-bearing log lines
- secrets accidentally present in repo or logs

## Execution Isolation

Workers should run with constrained execution environments.

Recommended controls:

- OCI containers
- read-only root filesystem where possible
- scoped input mounts only
- no implicit home-directory mounts
- network egress disabled by default
- separate service accounts or OS identities
- resource limits for CPU, RAM, GPU, and runtime

The worker should receive:

- immutable job spec
- explicit input refs
- output destination

It should not discover arbitrary extra data from the environment.

## Authentication And Authorization

The first secure deployment should include:

- authenticated broker API
- per-user or per-service principal identities
- role- or attribute-based authorization for job submission and result access
- scoped permissions for cancellation and artifact retrieval

At minimum, enforce:

- users can only fetch their own jobs unless explicitly permitted
- shared project jobs require project-level access
- administrative operations are distinct from ordinary usage

Current implementation notes:

- the HTTP broker supports `BROKER_AUTH_MODE=header` and `BROKER_AUTH_MODE=static_tokens`
- in static token mode, `Authorization: Bearer <token>` resolves to a configured actor and role
- `X-Broker-Role: admin` or an admin-mapped bearer token bypasses per-job owner checks
- submitted jobs persist `submitted_by`, and fetch, list, logs, result, and cancel flows are owner-scoped
- the stdio MCP server binds a session principal during `initialize`, with `BROKER_MCP_ACTOR` and `BROKER_MCP_ROLE` as unattended fallbacks

## Secrets Handling

The broker and workers may require credentials for:

- object storage
- databases
- private model registries
- cluster APIs

Rules:

- store secrets in a standard secret manager where possible
- avoid passing secrets through broad environment inheritance
- redact secrets from logs
- never include secrets in MCP responses

## Caching And Data Reuse Risks

Caching is a feature and a risk.

Risks:

- cross-project leakage
- stale results after policy changes
- retrieval of results created under broader permissions

Mitigations:

- namespace cache entries by tenant or project where required
- include policy mode in cache keys when it influences outputs
- support cache invalidation on policy revision
- keep audit records for cache hits as well as fresh runs

## Auditability

High-value events should be recorded:

- job submission
- policy allow or deny
- sensitive export override
- result retrieval
- cancellation
- artifact deletion
- cache hit on restricted inputs

The audit trail should be append-only or tamper-evident in production deployments.

Current implementation notes:

- audit events are emitted as JSON lines and include actor, role, action, outcome, job ID, and task type
- denied owner checks are recorded with `outcome=forbidden`
- sensitive result and log release paths can therefore be attributed to both HTTP and MCP principals
- each audit record includes `prev_hash` and `event_hash`, forming a hash chain over the append-only log

## Operational Security Defaults

Recommended defaults for the first release:

- local-only output mode on by default
- low-priority preemptible QoS for GPU jobs
- explicit allowlist of task types
- explicit allowlist of local model families
- JSON-schema validation on all worker results
- short retention for raw logs and transient artifacts

## Non-Goals

This system should not initially attempt to:

- provide a general data-loss-prevention platform for all enterprise data
- decide legal or compliance policy on its own
- trust remote model providers with raw internal data by default

It should provide safe primitives and enforcement hooks so organizations can apply their own policy requirements.
