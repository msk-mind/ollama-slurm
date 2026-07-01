# Cache Strategy

## Purpose

Caching is central to the value proposition of the broker. Without aggressive reuse, local compute simply becomes a second source of cost and latency. With effective caching, the broker can avoid rereading large inputs, repeating expensive parses, and rerunning model jobs unnecessarily.

This document defines:

- cache goals
- cache layers
- cache keys
- invalidation rules
- policy and tenant isolation requirements

## Design Goals

- `content-addressed`: cache entries should be derived from immutable input content where possible
- `multi-layer`: cache both final answers and expensive intermediate products
- `portable`: cache semantics should not depend on one backend
- `policy-safe`: cache reuse must not bypass confidentiality rules
- `observable`: cache hits and misses should be visible to users and operators

## Non-Goals

The cache should not:

- silently widen data access across users, projects, or tenants
- assume model outputs are equivalent across model or template changes
- serve stale results after meaningful input or policy changes

## Why Caching Matters Here

The broker is intended to reduce remote token usage by shifting expensive work local. Many of those expensive tasks have strong reuse patterns:

- the same repository is summarized repeatedly
- the same logs are analyzed repeatedly during incident triage
- the same parse or symbol index can support many searches
- embeddings can be reused for many downstream retrieval tasks

Without caching, the broker still helps privacy, but leaves significant efficiency on the table.

## Cache Layers

The broker should use multiple cache layers.

### L1: Job Result Cache

Stores final result payloads for exact-equivalent requests.

Examples:

- `repo_summary_v1` for the exact same repo snapshot
- `log_analysis_v1` for the exact same build log

### L2: Intermediate Analysis Cache

Stores reusable intermediate products.

Examples:

- repo chunk manifests
- AST or symbol indexes
- static-analysis raw findings
- filtered log timelines

This layer is often more valuable than only caching final answers because it supports many downstream tasks.

### L3: Embedding And Retrieval Cache

Stores generated embeddings and retrieval indexes.

Examples:

- per-chunk embeddings
- vector indexes
- nearest-neighbor metadata

### L4: Artifact Cache

Stores reusable generated artifacts that may not be final results.

Examples:

- candidate patches
- redacted excerpts
- generated summaries at different compression levels

### L5: RAG Compression Cache

Stores evidence-processing outputs used before remote synthesis.

Examples:

- file hash manifests
- chunk manifests
- ripgrep and BM25 retrieval results
- tree-sitter symbol indexes
- stack-trace/path retrieval outputs
- git diff/history retrieval outputs
- reranked chunk sets
- deduplicated evidence sets
- compressed evidence packs
- local model compression outputs

## Cache Key Strategy

Cache keys must capture all factors that can change semantic output.

At minimum include:

- task type
- input content hashes
- task params
- chunking or selection strategy
- retrieval strategy versions
- reranker version
- compressor model and template version
- model logical name and version
- runtime backend version when behavior differs materially
- prompt or template version
- output schema name and version
- relevant policy mode

Illustrative key material:

```json
{
  "task_type": "repo_summary",
  "input_hashes": ["sha256:repo_snapshot_hash"],
  "task_params": {
    "scope": "full_repo"
  },
  "chunking_version": "repo-chunker@1.0.0",
  "model": "qwen-coder-large@2026-06-01",
  "template": "repo-summary@1.0.0",
  "schema": "repo_summary_v1",
  "policy_mode": "restricted_safe_summary"
}
```

The cache key should be normalized and then hashed.

## Exact Cache Hits Vs Reusable Intermediates

Two types of reuse matter:

### Exact Hit

The broker can return an already validated result for the same logical request.

Examples:

- same log file, same schema, same model, same template

### Partial Reuse

The broker can reuse intermediate products but still needs to execute part of the pipeline.

Examples:

- a code search can reuse an existing symbol index
- a repo summary can reuse a chunk manifest
- root cause analysis can reuse previously extracted log events

The broker should surface these as distinct cases operationally even if the user-facing result is just faster completion.

## Input Hashing

Hashing rules should be explicit and deterministic.

Examples:

- file inputs: hash file bytes
- directory inputs: hash normalized manifest of file paths and content hashes
- repo inputs: hash tree snapshot, commit, or manifest depending on source of truth
- text inputs: hash normalized text bytes

For repositories, the best long-term approach is likely:

- canonical manifest of included files
- per-file content hashes
- optional commit SHA as advisory metadata, not sole identity

That avoids coupling cache correctness to VCS metadata alone.

## Invalidation Rules

Cache entries should be invalidated or bypassed when:

- input content changes
- task parameters change
- model version changes
- template or prompt version changes
- schema version changes
- policy mode changes
- worker implementation changes in ways that affect semantics

Some caches should also have TTLs:

- live logs
- rapidly changing generated artifacts
- volatile external-data enrichments

## Namespace Isolation

Cache reuse must respect organizational boundaries.

Recommended isolation dimensions:

- tenant
- project
- repository namespace
- confidentiality class

Examples:

- a restricted repo summary should not be reused across unrelated tenants
- a public dependency index may be globally shareable
- embeddings for proprietary code should remain namespace-scoped

## Policy Interaction

Policy must be enforced even on cache hits.

Rules:

- pre-release policy runs every time a result is fetched
- policy-sensitive dimensions belong in cache keys when they change the output
- broader-access results should not be blindly served to narrower viewers

Example:

- a cached result containing patch details may be valid for an engineer with approval, but only patch metadata may be releasable to another caller

## Suggested Cacheable Assets By Task

### `rag_compress`

Cache:

- input manifests
- chunks
- local indexes
- retrieval results
- reranker outputs
- deduplicated candidate evidence
- compressed evidence packs
- local model outputs

Layered key examples:

```text
chunks:v1:{namespace}:{corpus_hash}:{chunker}:{include_rules_hash}
index:v1:{namespace}:{corpus_hash}:{index_type}:{strategy_version}:{chunk_manifest_hash}
retrieval:v1:{namespace}:{corpus_hash}:{query_hash}:{strategy_set_hash}:{retrieval_budget}:{index_versions_hash}
rerank:v1:{namespace}:{retrieval_hash}:{reranker}:{reranker_version}:{top_k}
evidence_pack:v1:{namespace}:{rerank_hash}:{compressor}:{template_version}:{policy_mode}:{final_budget}:{schema}
model_output:v1:{namespace}:{model}:{model_version}:{prompt_template}:{input_hash}:{output_schema}:{policy_mode}
```

### `repo_summary`

Cache:

- repo manifest
- chunk manifest
- directory summaries
- final repo summary

### `code_search`

Cache:

- symbol index
- language map
- query result set if query is repeated

### `static_analysis`

Cache:

- analyzer raw outputs
- normalized issue set
- ranked summary

### `log_analysis`

Cache:

- parsed timeline
- error clusters
- redacted excerpts
- final summary

### `test_failure_analysis`

Cache:

- failing test extraction
- related-path search results
- root-cause candidate set

### `embedding_generation`

Cache:

- chunk manifests
- embeddings
- vector indexes

### `debug_with_local_context`

Cache:

- parsed test failures
- stack-trace path expansions
- related-path retrieval results
- recent git diff indexes
- evidence packs for repeated failure signatures

### `summarize_logs`

Cache:

- log file hashes
- parsed timelines
- repeated-line clusters
- stack-trace clusters
- root-error neighborhoods
- compressed log evidence packs

## Storage Recommendations

Suggested implementation split:

- metadata and cache index in PostgreSQL
- large blobs in object store or filesystem abstraction
- optional Redis only for hot status or ephemeral coordination

The broker should not require Redis for cache correctness.

## Observability

Expose at least:

- cache hit rate by task type
- exact-hit vs partial-reuse rate
- bytes or artifacts reused
- average latency improvement from cache
- invalidation reasons
- restricted-cache access counts

These metrics are necessary to prove the project is reducing repeated work.

## Failure Modes

Common cache failure scenarios:

- corrupted artifact blob
- missing blob referenced by valid metadata
- schema mismatch after upgrade
- stale result after policy evolution
- namespace leak due to bad keying

Mitigations:

- hash verification on artifact read
- background cache scrubbing
- versioned schemas and templates
- conservative cache bypass on ambiguity
- audit logs for restricted cache hits

## Recommended Initial Implementation

The first implementation should keep caching simple but correct.

Start with:

1. exact final-result caching
2. chunk-manifest caching for repos and documents
3. parsed-log intermediate caching
4. embedding index caching

Avoid starting with:

- overly clever probabilistic reuse
- semantic equivalence detection across different model versions
- cross-tenant cache sharing for restricted inputs

## Success Criteria

The cache strategy is working if:

- repeated repo and log tasks often avoid full recomputation
- cache correctness issues are rare and explainable
- users can tell when a result came from cache
- policy guarantees remain intact on cache hits
