# Slurm Assets

Slurm-specific deployment and execution assets live here.

Current contents:

- `broker_worker.slurm`: batch template for broker-managed worker execution
- `codex_llamacpp_proxy.py`: supported Codex-to-llama.cpp compatibility proxy
- `submit_llama.sh`: supported llama.cpp Slurm submission entrypoint
- `submit_ollama.sh`: supported Ollama Slurm submission entrypoint
- `connect_claude_llama.sh`: supported Claude-to-llama.cpp connector
- `llama_server.slurm`: supported llama.cpp batch template
- `ollama_server.slurm`: supported Ollama batch template
- `run_codex_llamacpp_proxy.sh`: supported launcher for that proxy using an existing `llama_server_connection_<job_id>.txt`
- `run_codex_llama.sh`: supported end-to-end launcher that submits, waits, proxies, invokes Codex, and cleans up

This directory is the home for broker-facing Slurm assets, distinct from the older root-level local model launch scripts.

Expected contents over time:

- batch templates
- submission helpers
- backend adapter support files
- example profiles for interactive and background classes

Broker tier mapping:

- `cpu-rag-indexing` should target your CPU-oriented partition
- `p40-rag-compression` should target your low-contention P40 partition
- `a100-reasoning` should target your scarce reasoning-capable GPU partition

The broker env example at `configs/broker/slurm-p40-a100.env.example` shows one concrete mapping for that layout.
Tier defaults can also inject `--nodelist` and `--constraint`, which is useful when the P40 lane is a known host set such as `pllimsksparky[1-4]`.

Separation of concerns:

- `broker/pkg/backends/slurm/` owns the Go adapter logic
- `deploy/slurm/` owns operational templates and scheduler-facing assets
- root-level `*.slurm` files are legacy direct-launch assets until they are migrated or retired

Codex compatibility path:

- start a llama.cpp server job with `deploy/slurm/submit_llama.sh`
- run `deploy/slurm/run_codex_llamacpp_proxy.sh --job-id <job_id>`
- point Codex at `http://127.0.0.1:1234/v1` using a custom provider config

Supported one-command flow:

- run `deploy/slurm/run_codex_llama.sh --config gpt-oss-20b.p40 --partition hpc --prompt "Reply with exactly: P40_OK"`
- this submits the Slurm job, waits for readiness, starts the proxy, invokes Codex, and cancels the job on exit unless `--keep-job` is set

Compatibility note:

- `../../run_codex_llama.sh` remains as a thin shim for older docs and scripts
- `../../submit_llama.sh`, `../../submit_ollama.sh`, `../../connect_claude_llama.sh`, `../../llama_server.slurm`, and `../../ollama_server.slurm` also remain as compatibility shims

This proxy currently normalizes:

- `/v1/models` into the catalog shape Codex expects
- `/v1/responses` tool declarations into plain `function` tools accepted by current `llama-server`
