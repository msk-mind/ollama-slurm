# Broker Config

This directory is for broker service configuration such as:

- database connection settings
- artifact storage settings
- backend enablement
- observability configuration
- authentication mode defaults
- audit log and maintenance settings
- MCP service identity fallbacks

Representative examples for this area would include:

- local development config
- single-node file-backed config
- first production-like Slurm deployment config

Current example:

- `slurm-p40-a100.env.example`: broker environment for a cluster where P40 nodes are the default RAG compression tier and A100 nodes are escalation-only reasoning capacity

This example is intentionally simple:

- `cpu-rag-indexing` maps to `BROKER_SLURM_PARTITION_CPU`
- `p40-rag-compression` maps to `BROKER_SLURM_PARTITION_P40`
- `a100-reasoning` maps to `BROKER_SLURM_PARTITION_A100`

The broker already maps retry recommendations onto those tier names. The env file closes the loop by giving each tier a concrete Slurm partition.

Optional tier-locality controls are also supported:

- `BROKER_SLURM_NODELIST_CPU`, `BROKER_SLURM_NODELIST_P40`, `BROKER_SLURM_NODELIST_A100`
- `BROKER_SLURM_CONSTRAINT_CPU`, `BROKER_SLURM_CONSTRAINT_P40`, `BROKER_SLURM_CONSTRAINT_A100`
- `BROKER_MODEL_PROFILE_CPU`, `BROKER_MODEL_PROFILE_P40`, `BROKER_MODEL_PROFILE_A100`
- `BROKER_RUNTIME_LLAMACPP_BASE_URL`, `BROKER_RUNTIME_LLAMACPP_TIMEOUT_SECONDS`
- `BROKER_RUNTIME_VLLM_BASE_URL`, `BROKER_RUNTIME_VLLM_TIMEOUT_SECONDS`
- `BROKER_RUNTIME_SGLANG_BASE_URL`, `BROKER_RUNTIME_SGLANG_TIMEOUT_SECONDS`

If a job request includes `execution_profile.nodelist` or `execution_profile.constraint`, that explicit override wins over the tier default.
If a job request omits `execution_profile.model`, the broker can fill it from the tier-local model profile defaults.
If a runtime endpoint is configured here, the broker stages it into `execution_plan.json` so workers do not need to discover it from ambient environment variables.
