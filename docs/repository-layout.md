# Repository Layout And Migration Plan

## Purpose

This repository currently centers on Slurm scripts and local model launch helpers. To become a general local AI compute broker, it needs a clearer separation between:

- control plane
- execution backends
- worker task implementations
- deployment assets
- documentation

This document describes the target repository structure and how the current files map into it.

## Target Layout

```text
.
├── broker/
│   ├── cmd/
│   │   ├── broker-server/
│   │   ├── broker-worker/
│   │   └── broker-cli/
│   ├── pkg/
│   │   ├── api/
│   │   ├── artifacts/
│   │   ├── audit/
│   │   ├── auth/
│   │   ├── backends/
│   │   │   ├── slurm/
│   │   │   ├── kubernetes/
│   │   │   ├── ray/
│   │   │   └── standalone/
│   │   ├── cache/
│   │   ├── mcp/
│   │   ├── models/
│   │   ├── policy/
│   │   ├── routing/
│   │   ├── schemas/
│   │   ├── scheduler/
│   │   └── store/
│   └── internal/
│       └── integrations/
├── workers/
│   ├── repo-summary/
│   ├── code-search/
│   ├── static-analysis/
│   ├── log-analysis/
│   ├── test-failure-analysis/
│   ├── root-cause-analysis/
│   ├── patch-generation/
│   └── embeddings/
├── containers/
│   ├── base/
│   ├── llama-cpp/
│   ├── vllm/
│   ├── sglang/
│   └── tooling/
├── deploy/
│   ├── slurm/
│   ├── systemd/
│   ├── kubernetes/
│   └── examples/
├── configs/
│   ├── broker/
│   ├── models/
│   ├── policies/
│   └── routing/
├── docs/
├── tests/
│   ├── unit/
│   ├── integration/
│   └── e2e/
└── examples/
    ├── mcp-clients/
    ├── prompts/
    └── workflows/
```

## Directory Responsibilities

### `broker/`

Owns the control plane.

Contents should include:

- MCP server implementation
- internal HTTP API
- backend adapters
- routing/planning logic
- policy enforcement
- job store and cache logic
- schema registration and validation

### `workers/`

Owns task-specific execution logic.

Each worker should:

- accept an immutable job spec
- resolve explicit inputs
- emit a schema-validated result
- write artifacts and metrics

Workers should not own:

- cluster scheduling
- authentication policy
- result authorization

### `containers/`

Owns OCI build contexts for worker runtimes and serving backends.

Examples:

- minimal Python worker base image
- llama.cpp runtime image
- vLLM runtime image
- SGLang runtime image
- tool-heavy image for parsing and static analysis

### `deploy/`

Owns operational deployment assets.

Examples:

- Slurm batch templates
- systemd units
- Kubernetes manifests or Helm charts
- environment examples

### `configs/`

Owns deployable configuration, distinct from code.

Examples:

- model profiles
- routing policies
- execution classes
- access and redaction policies

### `tests/`

Owns test coverage at three layers:

- unit tests for pure logic
- integration tests for broker and worker subsystems
- end-to-end tests for full job lifecycle

### `examples/`

Owns user-facing integration examples.

Examples:

- Copilot CLI MCP configuration
- Claude Code workflow examples
- Codex CLI integration examples
- sample prompts that trigger local delegation

## Mapping Current Files

The current repository contains useful first-generation assets. They should be retained, but moved into clearer homes.

### Slurm submission and runtime scripts

Current files:

- `submit_llama.sh`
- `submit_ollama.sh`
- `llama_server.slurm`
- `ollama_server.slurm`
- `list_servers.sh`
- `register_server.sh`

Target home:

- `deploy/slurm/`

Rationale:

- these are backend bootstrap and operational assets, not the product API

### Local model connection helpers

Current files:

- `connect_claude_llama.sh`
- `setup_claude_env.sh`

Target home:

- `examples/mcp-clients/legacy/`
- or `deploy/examples/legacy/`

Rationale:

- these are useful demonstrations of current workflow
- they are not the long-term northbound interface once MCP tools become primary

### Registry and dashboard

Current files:

- `registry_server.py`
- `dashboard.html`
- `llama-registry.service`

Target home:

- `broker/internal/integrations/legacy-registry/`
- `deploy/systemd/`

Rationale:

- the current registry is an operational helper
- some concepts may survive as a broker discovery/status surface
- the implementation itself should not define the future architecture

### Model configuration files

Current files:

- `model_configs/*.conf`

Target home:

- `configs/models/`

Rationale:

- model profiles are configuration data
- they should eventually be normalized into a broker-readable config format

### Current docs

Current files:

- `REGISTRY_SETUP.md`
- `QUICKSTART_REGISTRY.md`
- `EMAIL_NOTIFICATIONS.md`
- `NTFY_NOTIFICATIONS.md`
- `COMPLETE_FEATURE_SUMMARY.md`
- `IMPLEMENTATION_SUMMARY.md`
- `docs/confluence/claude-llama-local-setup.md`

Target home:

- `docs/operations/legacy/`
- `docs/architecture/`
- `docs/guides/`

Rationale:

- operational history is still useful
- architecture and implementation notes should be separated from product docs

## Suggested Phased Migration

### Phase 1: Introduce New Structure Without Breaking Existing Scripts

Actions:

- add design docs
- add top-level directories for `broker/`, `workers/`, `deploy/`, `configs/`, `examples/`, `tests/`
- leave existing scripts in place
- begin placing all new work in target locations

Goal:

- avoid churn while establishing architectural direction

### Phase 2: Move Operational Assets Behind Compatibility Wrappers

Actions:

- move Slurm assets into `deploy/slurm/`
- move model profiles into `configs/models/`
- leave root-level wrapper scripts if needed
- update docs to point to canonical paths

Goal:

- preserve usability while cleaning structure

### Phase 3: Introduce Control Plane And Worker Code

Actions:

- implement broker service under `broker/`
- implement initial worker tasks under `workers/`
- add integration tests
- formalize config loading and schema registration

Goal:

- establish the real product center of gravity

### Phase 4: Retire Legacy-Only Paths

Actions:

- remove redundant root-level scripts once broker-driven flows replace them
- archive or rewrite obsolete docs
- keep compatibility examples only where they still serve adoption

Goal:

- reduce conceptual confusion

## Recommended Near-Term File Moves

These are the most obvious eventual relocations:

- `model_configs/` -> `configs/models/`
- `llama_server.slurm` -> `deploy/slurm/llama_server.slurm`
- `ollama_server.slurm` -> `deploy/slurm/ollama_server.slurm`
- `submit_llama.sh` -> `deploy/slurm/submit_llama.sh`
- `submit_ollama.sh` -> `deploy/slurm/submit_ollama.sh`
- `llama-registry.service` -> `deploy/systemd/llama-registry.service`

I would not make those moves until the repo has at least minimal control-plane scaffolding, because otherwise the repo will look cleaner without actually becoming more coherent.

## Language And Build Boundaries

Recommended split:

- `broker/`: Go or Rust
- `workers/`: Python
- `deploy/`: shell, YAML, templates
- `configs/`: YAML, TOML, or JSON

Reasoning:

- control plane benefits from a compiled systems language
- workers benefit from the model and data tooling ecosystem in Python
- deployment assets should stay close to platform-native formats

## Success Criteria For Repository Structure

The repository structure is working if:

- a new contributor can tell what the product is within a few minutes
- control-plane code is easy to distinguish from worker logic
- backend-specific assets do not dominate the top level
- documentation clearly separates target architecture from current operational scripts
- adding a new backend or task type does not require reorganizing the repo again

