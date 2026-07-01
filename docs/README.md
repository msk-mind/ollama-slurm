# Documentation

## Current State

This repository now contains both:

- a working first broker implementation under `broker/`
- design and planning documents for the broader product direction

The implemented baseline currently includes:

- broker HTTP server
- stdio MCP server
- Slurm backend adapter
- schema-validated worker results
- cache lookup and reuse
- worker progress heartbeats
- sensitive result and log release filtering
- tamper-evident audit logging with verification and maintenance tooling

## Design Docs

These documents describe the target architecture for evolving the current implementation into a general local AI compute broker for MCP-capable agents.

- [Broker Quickstart](./quickstart.md)
- [Architecture](./architecture.md)
- [RAG Compression](./rag-compression.md)
- [MCP Tools And Broker API](./mcp-tools.md)
- [Data Model](./data-model.md)
- [Task And Result Schemas](./task-schemas.md)
- [Backend Interface](./backend-interface.md)
- [Parallel Execution](./parallel-execution.md)
- [Worker Runtime](./worker-runtime.md)
- [Cache Strategy](./cache-strategy.md)
- [Policy Rules](./policy-rules.md)
- [Repository Layout And Migration Plan](./repository-layout.md)
- [Security Model](./security-model.md)
- [Threat Model](./threat-model.md)
- [Operations](./operations.md)
- [MVP Plan](./mvp-plan.md)
- [Roadmap](./roadmap.md)

## Existing Operational Docs

These documents describe the current llama.cpp and Slurm workflow already present in the repository.

- [Claude CLI local setup](./confluence/claude-llama-local-setup.md)
- [Registry setup](../REGISTRY_SETUP.md)
- [Quickstart registry](../QUICKSTART_REGISTRY.md)
- [Email notifications](../EMAIL_NOTIFICATIONS.md)
- [ntfy notifications](../NTFY_NOTIFICATIONS.md)

## Positioning

The design docs treat the current scripts and model configs as the starting point for a broader broker architecture:

- current scripts are operational bootstrap assets
- Slurm is the first backend
- local model launch is one worker/runtime mechanism
- the primary product becomes the broker control plane and its job/result contracts

## Reading Order

For a quick orientation:

1. read [Broker Quickstart](./quickstart.md)
2. read [Architecture](./architecture.md)
3. read [MCP Tools And Broker API](./mcp-tools.md)
4. read [Operations](./operations.md)
5. read [Roadmap](./roadmap.md)
