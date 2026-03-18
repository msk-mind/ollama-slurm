# Copilot Instructions

This repository provides SLURM integration for running LLM inference servers (llama.cpp and Ollama) on HPC clusters and connecting Claude CLI to them.

## Build, test, and lint

**Registry server tests** (the only automated tests):
```bash
# Run full suite
.venv/bin/python3 -m pytest tests/test_registry.py -v

# Run a single test class
.venv/bin/python3 -m pytest tests/test_registry.py::TestRegistration -v

# Run a single test
.venv/bin/python3 -m pytest tests/test_registry.py::TestRegistration::test_update_preserves_start_time -v
```

**Set up the venv first** if it doesn't exist:
```bash
python3 -m venv .venv && .venv/bin/pip install -r registry/requirements.txt pytest
```

**Run the registry server locally:**
```bash
REGISTRY_FILE=/tmp/test_registry.json .venv/bin/python3 registry/registry_server.py --host 127.0.0.1 --port 5555
```

There are no linters or build steps — shell scripts and Python run directly.

## Architecture

Two parallel LLM server implementations share the same overall structure:

```
submit_{llama,ollama}.sh          # User entry point: parse args, build sbatch command
  └─ {llama,ollama}_server.slurm  # Actual SLURM job: start server, health-check, integrate
       ├─ register_server.sh       # POST to registry (llama.cpp only)
       ├─ send_notification.sh     # Email (llama.cpp only)
       └─ send_ntfy_notification.sh# Push via ntfy.sh (llama.cpp only)

connect_claude_llama.sh           # Source connection file → setup_claude_env.sh → exec claude
setup_claude_env.sh               # Sets ANTHROPIC_BASE_URL, ANTHROPIC_AUTH_TOKEN=ollama

registry/
  registry_server.py              # Flask registry for server discovery without shared filesystem
  dashboard.html                  # Web UI served at registry /
  requirements.txt                # Flask dependency
  Dockerfile                      # Container deployment
  llama-registry.service          # systemd unit
  install_registry.sh             # Deploy as systemd service (requires sudo)

list_servers.sh                   # Query registry CLI
model_configs/*.conf              # Sourced shell scripts: per-model GPU/memory/arg presets
tests/                            # test_registry.py (pytest), test_registry.sh (integration)
docs/                             # Extended documentation (REGISTRY_SETUP.md, GPU_TYPES.md, etc.)
```

**llama.cpp vs Ollama:** llama.cpp has full integration (registry, email, ntfy, model config library). Ollama is simpler — no registry registration, no notifications, Ollama manages its own model format.

**The connection file** (`{llama,ollama}_server_connection_{JOB_ID}.txt`) is created by the SLURM job in `SLURM_SUBMIT_DIR` and sourced by `connect_claude_*.sh`. It exports `LLAMA_SERVER_HOST` and `LLAMA_SERVER_PORT`. The registry is an alternative when a shared filesystem isn't available.

## GPU memory and model sizing

| GPU  | VRAM  | Max per node | Notes |
|------|-------|--------------|-------|
| A100 | 80 GB | 4 | Preferred; fits all current models |
| V100 | 16 GB | 4 | Multi-GPU only; context must be reduced |
| P40  | 24 GB | 1 | Single GPU only; limited context |

**Available config profiles and their VRAM requirements:**

| Config | GPUs | Context | VRAM per GPU | Constraint |
|--------|------|---------|--------------|------------|
| `qwen3-30b.a100` | 1× A100 | 128K | ~44 GB | — |
| `qwen3-30b.v100` | 4× V100 | 64K | ~7.75 GB | Context halved vs A100 |
| `qwen3-30b.p40` | 1× P40 | 16K | ~21.25 GB | Context severely limited |
| `qwen3-coder-30b.a100` | 1× A100 | 128K | ~58 GB | Q8_0 quant |
| `qwen3-coder-30b.v100` | 4× V100 | 32K | ~9.6 GB | No P40 (32 GB model > 24 GB) |
| `qwen3-80b.a100` | 4× A100 | 128K | ~24.75 GB | A100 only (47 GB model > 4×V100) |
| `glm-4.7.a100` | 2× A100 | 128K | — | DeepSeek2 MLA + 64 experts, needs >80 GB total |

**Why some profiles don't exist:**
- `qwen3-80b` on V100/P40 — 47 GB model exceeds 64 GB (4× V100) with any usable context
- `qwen3-coder-30b` on P40 — 32 GB model exceeds 24 GB single-GPU limit
- `glm-4.7` on V100/P40 — DeepSeek2 MLA architecture + MoE compute buffers require >80 GB; reduced context is too small for Claude CLI

**VRAM budget rule of thumb:** `total_vram = model_size + kv_cache`. KV cache at 128K context is ~26 GB for 30B models, ~52 GB for 80B. Reducing context halves KV cache size proportionally.

When adding a new model config, check `GPU_TYPES.md` for the full sizing table and per-GPU breakdown.

## Key conventions

**Port discovery** — both SLURM scripts use this idiom to bind to an available ephemeral port:
```bash
PORT=$(python3 -c 'import socket; s=socket.socket(); s.bind(("", 0)); print(s.getsockname()[1]); s.close()')
```

**Model configs** live in `model_configs/` and are plain shell scripts sourced by `submit_llama.sh`. They set `MODEL_FILE`, `CPUS`, `MEM`, `GPUS`, `GPU_TYPE`, `CONTEXT_SIZE`, `N_GPU_LAYERS`, and `EXTRA_ARGS`. Adding a new model means adding a `.conf` file there — no code changes needed. Config naming convention: `{model-name}.{gpu-type}.conf` (e.g. `qwen3-30b.a100.conf`).

**Environment variable propagation** — `submit_llama.sh` passes variables into SLURM jobs via `sbatch --export=ALL,...`. Every integration (registry URL, notification targets, model settings) flows through env vars, not files.

**Optional integrations are non-blocking** — in `llama_server.slurm`, all three hooks (register, email, ntfy) are guarded by env var checks and called with `|| true` so a failing hook never kills the server.

**Registry data flow:**
- SLURM job POSTs to `$REGISTRY_URL/servers/$SLURM_JOB_ID` on startup
- Preserves `start_time` across updates (updates only refresh `last_updated`)
- DELETE on job exit (trap in `llama_server.slurm`)
- Stale entries auto-cleaned after `MAX_AGE_HOURS = 48`
- `REGISTRY_FILE` env var overrides the default storage path (needed for Docker/systemd where `~/.cache/` is inaccessible); `registry/llama-registry.service` sets it to `/opt/llama-registry/data/registry.json`

**Claude CLI auth** — local llama.cpp servers require `ANTHROPIC_AUTH_TOKEN=ollama` (a dummy value) and `ANTHROPIC_BASE_URL` pointing at the server. This is set in `setup_claude_env.sh`.

**Log file naming** uses SLURM's `%j` token: `llama_server_%j.log` / `llama_server_%j.err`. Connection files use the resolved `$SLURM_JOB_ID`.

**Tests are fully isolated** — `tests/test_registry.py` resets `rs.servers = {}` and uses a fresh temp file per test via `setUp`/`tearDown`. The file inserts `registry/` into `sys.path` at import time so `import registry_server as rs` resolves correctly.
