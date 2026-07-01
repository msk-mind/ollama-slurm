# Policy Rules

## Purpose

The security model establishes principles. This document makes those principles operational by defining how the broker should make allow, deny, redaction, and export decisions.

The goal is not to invent a full enterprise compliance framework. The goal is to define clear broker behavior for local AI delegation.

## Policy Model

Every job should be evaluated twice:

1. `pre-execution`
2. `pre-release`

This matters because a job may be safe to run locally but not safe to expose remotely in raw form.

## Policy Decision Outcomes

The broker should support four primary outcomes:

- `allow`
- `deny`
- `allow_with_redaction`
- `allow_with_approval`

Meaning:

- `allow`: run and release within normal rules
- `deny`: do not run or do not release
- `allow_with_redaction`: run or release only after field filtering or artifact substitution
- `allow_with_approval`: require explicit user or admin override

## Policy Inputs

Policy should evaluate at least:

- actor identity
- tenant or project
- task type
- input classifications
- input locations
- requested output schema
- requested execution backend
- requested remote disclosure mode
- target remote provider or model when relevant
- explicit override flag

Optional inputs:

- time-of-day or environment restrictions
- repository tags
- incident response mode
- break-glass role

## Default Policy Posture

Recommended defaults:

- local execution is allowed for approved task types
- raw remote export is denied by default for non-public inputs
- remote MCP clients receive compressed evidence packs by default, not raw repositories, logs, documents, chunks, embeddings, or indexes
- redacted summaries are preferred over raw excerpts
- embedding generation is local-only unless explicitly approved
- chunk text, local indexes, retrieval intermediates, and uncompressed model context remain local-only unless an explicit raw export approval exists
- patch diffs may be exportable, but only after redaction checks

## Pre-Execution Policy

Questions to answer before a job runs:

- is the actor allowed to analyze this input?
- is the task type allowed for this project?
- is the requested backend allowed for this data class?
- is the requested model profile allowed for this confidentiality level?
- is the requested runtime or container image approved?

Example pre-execution decisions:

- allow `log_analysis` on restricted logs using a local model
- deny `document_summary` if the requested backend is an unapproved external service
- allow `repo_summary` only on a namespace-scoped worker image

## Pre-Release Policy

Questions to answer before a result is returned to the MCP client:

- can the result payload be returned as-is?
- do any fields contain raw sensitive data?
- should evidence remain as artifact references only?
- should excerpts be redacted, removed, or replaced?
- is explicit user approval required?

Example pre-release decisions:

- allow summary text but remove raw stack trace lines
- return patch metadata but keep full diff local
- deny release if the result includes PHI-bearing log excerpts
- redact path-like fields and withhold artifact payloads for sensitive result fetches unless a scoped override exists

## Classification Rules

The broker should operate on a small initial classification set:

- `public`
- `internal`
- `restricted`
- `phi`
- `secret_adjacent`

Baseline behavior:

- `public`: export generally allowed
- `internal`: export of compact summaries allowed, raw content restricted
- `restricted`: local-only by default, summary export by policy
- `phi`: local-only unless explicit approved workflow exists
- `secret_adjacent`: local-only raw handling and strong redaction checks

## Task-Level Policy Defaults

Recommended starting defaults:

### `repo_summary`

- allow local execution
- allow compact structured summary release
- deny raw file export by default

### `code_search`

- allow local execution
- allow path and symbol metadata release
- allow excerpt references
- deny large inline excerpts by default

### `static_analysis`

- allow local execution
- allow ranked issue summaries
- allow path and line hints
- deny raw source blocks unless approved

### `log_analysis`

- allow local execution
- allow summarized findings
- redact or remove sensitive log lines
- deny full raw log release by default
- deny `fetch_job_logs` for `restricted`, `phi`, and `secret_adjacent` inputs unless an explicit scoped override is present

### `test_failure_analysis`

- allow local execution
- allow failing test names and non-sensitive paths
- treat stack traces and fixture data as potentially sensitive

### `root_cause_analysis`

- allow local execution
- allow hypothesis summaries and evidence references
- require redaction for raw excerpts

### `rag_compress`

- allow local execution for approved input scopes
- allow release of `rag_evidence_pack_v1` after schema validation, evidence-reference validation, token-budget validation, and redaction checks
- keep raw chunks, embeddings, local indexes, retrieval results, rerank scores, and compressor prompts local-only by default
- require approval before including raw excerpts from `restricted`, `phi`, or `secret_adjacent` inputs

### `debug_with_local_context`

- allow local execution over repositories, logs, stack traces, test outputs, and git history the actor can access
- allow release of failure signatures, hypotheses, confidence scores, and evidence references
- require redaction for stack traces, fixture data, request payloads, and environment values
- deny release if the evidence pack contains unreferenced claims about sensitive inputs

### `summarize_logs`

- allow local execution on large logs
- allow release of deduplicated error clusters, timestamp ranges, phase summaries, and evidence references
- redact or remove raw log lines containing credentials, PHI-like identifiers, customer data, or hostnames when configured sensitive
- deny full raw log export by default

### `inspect_repo`

- allow local execution on authorized repositories
- allow release of subsystem names, path metadata, symbol metadata, callgraph summaries, and evidence references
- deny raw source blocks by default
- require approval for raw excerpts from non-public repositories

### `embedding_generation`

- allow local execution
- deny export of vectors or chunk text by default
- allow release of index metadata only

### `patch_generation`

- allow local execution
- allow patch metadata release
- treat full patch diff as approval-gated when based on restricted inputs
- require every proposed change to cite local evidence references or validation results

## Output Field Rules

Schema design should support per-field handling.

Recommended categories:

- `safe_summary_field`
- `path_metadata_field`
- `raw_excerpt_field`
- `artifact_reference_field`
- `evidence_reference_field`
- `evidence_pack_field`
- `generated_patch_field`
- `diagnostic_field`

Example policy actions:

- keep `summary`
- keep `path`
- replace `raw_excerpt` with `artifact_ref`
- remove `diagnostic_field` if it contains credentials
- downgrade `generated_patch_field` to approval-required

## Redaction Rules

The first release should define simple deterministic redaction behavior.

Possible redaction targets:

- email addresses
- access tokens
- bearer tokens
- API keys
- MRNs or other PHI-like identifiers
- hostnames if considered sensitive
- full absolute paths outside approved scope

Redaction actions:

- replace token with marker
- remove field
- trim excerpt to bounded safe context
- convert inline content into local-only artifact reference

## Approval Model

Some actions should require explicit override.

Examples:

- exporting raw repo excerpts from restricted code
- returning a full patch diff generated from a proprietary repository
- exposing unredacted failure logs containing customer or clinical data

Approval should be:

- explicit
- scoped to the current job or result
- auditable
- time-bounded if possible

## Cache Policy

Policy must influence cache reuse.

Rules:

- results generated under broader access should not be silently reused for narrower viewers
- cache keys should include policy-relevant dimensions when output differs
- restricted artifacts should remain namespace-scoped where required
- cache-hit retrieval should still trigger pre-release policy checks

## Audit Requirements

Policy-related events to record:

- deny decisions
- approval-required decisions
- approval grants
- redaction actions
- raw export overrides
- restricted cache hits

## Suggested Policy Representation

The broker should use declarative policy where possible.

A useful representation would express:

- subject
- resource
- action
- context
- decision

Example conceptual rule:

```text
If input.classification in {restricted, phi}
and requested_release_mode == raw
then decision = allow_with_approval
```

This can later map to:

- OPA/Rego
- embedded rule engine
- database-backed policy tables

## Recommended Initial Rule Set

For the first implementation:

1. Deny raw export for `restricted`, `phi`, and `secret_adjacent` by default.
2. Allow compact summary export for `internal` and `restricted` if schema fields are marked safe.
3. Allow compact evidence-pack export only after JSON schema validation, evidence-reference validation, token-budget validation, and redaction checks pass.
4. Require approval for raw excerpts and full patch diffs derived from restricted inputs.
5. Keep raw chunks, embeddings, indexes, retrieval results, rerank results, and compressor prompts local-only by default.
6. Require pre-release redaction pass on all log-derived outputs.
7. Block any result that fails schema validation, evidence-reference validation, token-budget validation, or redaction checks.

## Open Questions

These should be resolved before production use:

- should patch diffs be considered raw content or derived safe output?
- should path names themselves be treated as sensitive in some environments?
- how should mixed-classification inputs be labeled when one artifact contains both public and restricted data?
- should policy be evaluated against the remote provider identity, the remote model family, or both?
