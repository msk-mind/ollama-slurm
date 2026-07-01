# Deploy

Deployment assets live here.

Current contents:

- `local/`: direct-execution assets for laptops and single hosts
- `slurm/`: batch templates and backend-specific operational assets

Intended future contents:

- `systemd/`: broker service units
- `kubernetes/`: deployment manifests or charts
- `examples/`: deployment examples and environment templates

Current repository state:

- root-level scripts remain in place as legacy operational assets for direct llama.cpp and Ollama workflows
- broker-oriented deployment assets should land here instead of further expanding the root directory

Near-term priorities for this directory:

- broker server startup examples
- broker MCP service launch examples
- Slurm submission templates for broker workers
- environment-variable templates for first deployments
- supported compatibility shims for external agent clients when backend runtimes differ slightly in protocol behavior
