# Threat Model

## Purpose

This document identifies the main assets, trust boundaries, threats, and mitigations for the local AI compute broker.

It is not a full formal security review. It is the architectural threat model that should guide implementation and later review.

## Security Objective

Allow remote MCP-capable agents to orchestrate local compute while preventing accidental or unauthorized disclosure of raw sensitive data.

## Primary Assets

The system must protect:

- source repositories
- proprietary documents
- build and runtime logs
- PHI or regulated data where present
- generated embeddings and indexes
- candidate patches
- artifact metadata
- credentials for storage, database, and cluster access
- audit trail integrity

## Actors

### Trusted Actors

- authorized developer using MCP-capable agent tooling
- broker service
- approved worker runtime
- approved backend scheduler

### Semi-Trusted Actors

- remote frontier model acting as orchestrator

This actor is trusted to assist, but not trusted to automatically receive all local raw data.

### Potential Adversaries

- unauthorized internal user
- compromised worker container
- compromised broker host
- malicious or buggy task implementation
- operator error causing overbroad export or cache reuse

## Trust Boundaries

### Boundary 1: Remote Agent To Broker

Risk:

- remote agent requests overly broad access or tries to obtain raw data

Control:

- broker policy, schema filtering, explicit approval requirements

### Boundary 2: Broker To Backend

Risk:

- scheduler submission metadata leaks sensitive context
- job tracking becomes inconsistent

Control:

- minimal submission metadata, durable backend run tracking, reconciliation

### Boundary 3: Backend To Worker Runtime

Risk:

- worker gains more filesystem or network access than intended

Control:

- scoped mounts, isolated runtime, explicit input manifests, egress restrictions

### Boundary 4: Worker To Artifact Storage

Risk:

- sensitive artifacts stored insecurely or with incorrect retention

Control:

- typed artifacts, classification-aware retention, access control, hash validation

### Boundary 5: Broker To Result Consumer

Risk:

- raw sensitive output accidentally returned to remote orchestrator

Control:

- pre-release policy evaluation, redaction, approval gates

## Key Threats

### Threat 1: Raw Source Code Leakage

Scenario:

- a repo analysis task returns large source excerpts or full diffs to a remote provider without explicit approval

Mitigations:

- local-only default
- pre-release policy checks
- schema design favoring artifact refs over inline excerpts
- explicit approval for raw export

### Threat 2: Sensitive Log Leakage

Scenario:

- logs contain PHI, tokens, secrets, or internal hostnames and are returned inline

Mitigations:

- deterministic redaction pass
- classification-aware release rules
- deny raw log export by default

### Threat 3: Cross-Tenant Cache Leakage

Scenario:

- a cached restricted result is reused for another tenant or project

Mitigations:

- namespace-aware cache keys
- pre-release policy on cache hits
- audit logging for restricted cache access

### Threat 4: Overprivileged Worker

Scenario:

- worker scans unrelated directories or uses network egress to exfiltrate data

Mitigations:

- scoped input manifests
- container isolation
- egress disabled by default
- least-privilege runtime identity

### Threat 5: Artifact Store Exposure

Scenario:

- sensitive excerpts or patches remain in storage longer than intended or with overly broad access

Mitigations:

- retention by classification
- access control on artifacts
- encryption at rest where required

### Threat 6: Forged Or Unauthorized Job Access

Scenario:

- one user fetches or cancels another user's job

Mitigations:

- authenticated broker API
- per-job authorization checks
- project or tenant scoping

### Threat 7: Policy Bypass Through Worker Output

Scenario:

- worker emits sensitive data in an unexpected field or malformed structure

Mitigations:

- schema validation
- field-level policy handling
- deny on invalid or unclassified outputs

### Threat 8: Scheduler Drift And Orphaned Jobs

Scenario:

- broker loses track of running jobs after restart or backend inconsistency

Mitigations:

- durable DB state
- backend reconciliation
- startup recovery logic

### Threat 9: Secret Exposure In Logs

Scenario:

- credentials for artifact storage or database appear in worker or broker logs

Mitigations:

- secret manager usage where possible
- log redaction
- minimal environment inheritance

## Assumptions

The architecture currently assumes:

- the local cluster and scheduler are organizationally controlled
- the broker runs in a trusted environment
- the remote orchestrator is not treated as fully trusted for raw data
- approved workers are built from controlled source

If any of these assumptions do not hold, the control set must be strengthened accordingly.

## Security Properties To Preserve

The implementation should preserve these properties:

- raw restricted data does not leave local environment by default
- result release is mediated by broker policy, not worker choice
- cache reuse does not widen access
- scheduler choice does not change confidentiality guarantees
- audit trail captures sensitive decisions and overrides

## High-Priority Mitigations For MVP

These controls matter most for the first implementation:

1. authenticated broker API
2. pre-execution and pre-release policy checks
3. schema-validated worker outputs
4. scoped worker inputs and output destinations
5. namespace-safe cache keys
6. audit logging for approvals and restricted result access
7. deterministic redaction for logs and excerpts

## Review Triggers

The threat model should be revisited when:

- a new backend is added
- a new task type exports materially different artifacts
- cross-tenant deployment is introduced
- remote raw export rules are expanded
- a new model runtime changes output structure or tool behavior

