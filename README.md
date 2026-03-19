# llama.cpp SLURM Integration for Claude CLI

Scripts to run llama.cpp server on SLURM and connect Claude CLI to it.

**Features:**

- Automated server submission to SLURM with optimized settings
- Central registry for discovering servers without shared filesystem access
- Pre-configured model profiles for common LLMs
- Automatic cleanup and health monitoring
- Email notifications when servers are ready
- Push notifications via ntfy.sh

## Quick Start

### For Users with Shared Filesystem Access

1. **Submit llama.cpp server with a saved configuration:**

   ```bash
   ./submit_llama.sh --config qwen3-30b
   ```

2. **Wait for server to start (check logs):**

   ```bash
   tail -f llama_server_<job_id>.log
   ```

3. **Connect an AI coding assistant to the server:**

   ```bash
   ./connect_claude_llama.sh <job_id>    # Claude CLI
   ./connect_openclaw_llama.sh <job_id>  # OpenClaw
   ```

### For Users Without Shared Filesystem Access

1. **List available servers from the registry:**

   ```bash
   curl http://your-registry-server:5000/servers | jq
   ```

2. **Create SSH tunnel to the server:**

   ```bash
   ssh -L 8080:<host>:<port> your-login-node
   ```

3. **Connect Claude to the tunneled server:**

   ```bash
   export ANTHROPIC_BASE_URL="http://localhost:8080"
   claude
   ```

See [REGISTRY_SETUP.md](docs/REGISTRY_SETUP.md) for registry server setup.

## Usage

### Submit llama.cpp Server

```bash
./submit_llama.sh [OPTIONS]
```

**Options:**

- `--config NAME` - Use predefined model config
- `--list-configs` - List available model configurations
- `--model FILE` - Path to GGUF model file
- `--time TIME` - Time limit (default: 8:00:00)
- `--no-time-limit` - Remove time limit entirely
- `--partition PART` - SLURM partition (default: none)
- `--qos QOS` - SLURM QOS (default: none)
- `--cpus N` - Number of CPUs (default: 8)
- `--mem SIZE` - Memory allocation (default: 32G)
- `--gpus N` - Number of GPUs (default: 1)
- `--context N` - Context size (default: 131072)
- `--gpu-layers N` - GPU layers, -1 for all (default: -1)
- `--extra-args STR` - Additional llama-server arguments
- `--email EMAIL` - Email address for notification when server is ready
- `--ntfy-topic TOPIC` - Ntfy topic for push notifications (default: llama-servers)
- `--ntfy-server URL` - Ntfy server URL (default: <https://ntfy.sh>)
- `--help, -h` - Show help message

**Examples:**

```bash
# Use saved configuration
./submit_llama.sh --config qwen3-30b

# List available configurations
./submit_llama.sh --list-configs

# No time limit with saved config
./submit_llama.sh --config glm-4.7 --no-time-limit

# With email notification
./submit_llama.sh --config qwen3-30b --email user@example.com

# With ntfy push notification
./submit_llama.sh --config qwen3-30b --ntfy-topic my-llama-servers

# Custom model file
./submit_llama.sh --model ~/.cache/llama.cpp/model.gguf

# Custom settings
./submit_llama.sh --model model.gguf --gpus 2 --context 16384
```

### Model Configurations

Model configurations are stored in `model_configs/*.conf`. Each config sets optimal parameters based on [claude-code-tools recommendations](https://github.com/pchalasani/claude-code-tools/blob/main/docs/local-llm-setup.md).

**Server settings (all models):**

- Context: 131072 tokens (128K) on A100 profiles — **minimum 32K needed for Claude CLI, 128K strongly recommended**
- Batch size: 32768
- Ubatch: 1024
- Parallel slots: 1
- Jinja templating: enabled

**Create a new configuration:**

```bash
cat > model_configs/my-model.conf <<EOF
MODEL_FILE="~/.cache/llama.cpp/my-model.gguf"
CPUS=16
MEM="64G"
GPUS=2
CONTEXT_SIZE=131072
N_GPU_LAYERS=-1
EXTRA_ARGS=""
EOF
```

**Available configurations:**

| Config | Model | GPUs | Context | GPU Requirements | Notes |
|--------|-------|------|---------|------------------|-------|
| `qwen3-30b.a100` | Qwen3 30B Q4_K_M | 1 | 128K | 1x A100 80GB | ~44GB VRAM |
| `qwen3-30b.v100` | Qwen3 30B Q4_K_M | 4 | 64K | 4x V100 16GB | ⚠️ Reduced context |
| `qwen3-30b.p40` | Qwen3 30B Q4_K_M | 1 | 16K | 1x P40 24GB | ❌ Too small for Claude CLI |
| `qwen3-coder-30b.a100` | Qwen3 Coder 30B Q8_0 | 1 | 128K | 1x A100 80GB | ~58GB VRAM |
| `qwen3-coder-30b.v100` | Qwen3 Coder 30B Q8_0 | 4 | 32K | 4x V100 16GB | ⚠️ Marginal context |
| `qwen3-next-80b.a100` | Qwen3-Next 80B Q4_K_XL | 4 | 128K | 4x A100 80GB | Instruct, ~47GB model |
| `qwen3-next-80b.a100.thinking` | Qwen3-Next 80B Q4_K_XL | 4 | 128K | 4x A100 80GB | Thinking variant, emits `<think>` traces |
| `glm-4.7.a100` | GLM-4.7 Flash Q4_K_M | 2 | 128K | 2x A100 80GB | DeepSeek2 MLA, ~16GB model |
| `glm-z1-32b.a100` | GLM-Z1 32B Q4_K_M | 1 | 128K | 1x A100 80GB | Dense reasoning model, emits `<think>` traces |
| `glm-z1-32b.v100` | GLM-Z1 32B Q4_K_M | 4 | 32K | 4x V100 16GB | ⚠️ Native 32K limit — marginal for Claude CLI |

**GPU VRAM Notes:**

- **A100 80GB**: Can run all configurations at full context — **recommended for Claude CLI**
- **V100 16GB**: Multi-GPU split with reduced context; 32K–64K context is the functional minimum for Claude CLI
- **P40 24GB**: 16K context only — too small for Claude CLI in practice
- KV cache at 128K context requires ~13GB VRAM per GPU; reduced context profiles use proportionally less

### Connect an AI Coding Assistant

The llama.cpp server exposes an OpenAI-compatible API, so any OpenAI-compatible client works. Two are supported out of the box:

| Client | Script | Env setup |
|--------|--------|-----------|
| **Claude CLI** (`claude`) | `connect_claude_llama.sh` | `setup_claude_env.sh` |
| **OpenClaw** (`openclaw`) | `connect_openclaw_llama.sh` | `setup_openclaw_env.sh` |

#### Claude CLI

```bash
./connect_claude_llama.sh <job_id> [working_directory]

# Examples
./connect_claude_llama.sh 2883398
./connect_claude_llama.sh 2883398 /path/to/my/project
./connect_claude_llama.sh 2883398 ~/code --resume abc123
```

**Note**: On first connection to a directory, Claude will ask you to confirm trust of the workspace. You can pre-approve with: `claude trust /path/to/workspace`

**Known Issue**: Some directories may cause Claude to hang indefinitely with local llama.cpp servers. If this happens, try a different working directory or use the official Anthropic API for that directory.

#### OpenClaw

```bash
./connect_openclaw_llama.sh <job_id> [working_directory]

# Examples
./connect_openclaw_llama.sh 2883398
./connect_openclaw_llama.sh 2883398 /path/to/my/project
```

### Manual Connection

If you prefer to set environment variables yourself:

```bash
source llama_server_connection_<job_id>.txt

# Claude CLI
source setup_claude_env.sh
claude

# OpenClaw
source setup_openclaw_env.sh
openclaw
```

## Managing Jobs

**Check job status:**

```bash
squeue -u $USER -n llama-server
```

**List available servers (via registry):**

```bash
./list_servers.sh
./list_servers.sh --owner $USER
```

**View logs:**

```bash
tail -f llama_server_<job_id>.log
```

**Cancel job:**

```bash
scancel <job_id>
```

## Server Discovery & Registry

Central registry service for discovering servers without shared filesystem access.

**Deploy the registry** (pick one):

```bash
# Systemd (production, as root)
sudo ./install_registry.sh

# Docker
docker build -t llama-registry . && \
  docker run -d --name llama-registry --restart unless-stopped \
    -p 5000:5000 -v llama-registry-data:/data llama-registry
```

**Enable auto-registration** in SLURM jobs:

```bash
export REGISTRY_URL="http://your-registry-server:5000"
./submit_llama.sh --config qwen3-30b
```

Servers auto-register on startup and unregister on shutdown. See [REGISTRY_SETUP.md](docs/REGISTRY_SETUP.md) for the full setup guide.

## Email Notifications

Get notified by email when your server is ready:

```bash
# Specify email when submitting
./submit_llama.sh --config qwen3-30b --email user@example.com

# Or set default email in your environment
export NOTIFY_EMAIL="user@example.com"
./submit_llama.sh --config qwen3-30b
```

The email includes:

- Server connection details (host, port, job ID)
- Multiple connection methods (direct, SSH tunnel, web dashboard)
- Commands to connect Claude
- Monitoring and management commands

**Requirements:**

- System must have `mail`, `sendmail`, or `mutt` installed
- SMTP service configured on compute nodes or login nodes

## Push Notifications (ntfy)

Get instant push notifications to your phone or desktop when servers are ready using [ntfy.sh](https://ntfy.sh):

```bash
# Use default public ntfy.sh server
./submit_llama.sh --config qwen3-30b --ntfy-topic my-llama-servers

# Use custom ntfy server
./submit_llama.sh --config qwen3-30b --ntfy-topic servers --ntfy-server https://ntfy.mycompany.com

# Set default topic in environment
export NTFY_TOPIC="my-llama-servers"
./submit_llama.sh --config qwen3-30b
```

**To receive notifications:**

1. Install ntfy app on your phone ([iOS](https://apps.apple.com/us/app/ntfy/id1625396347), [Android](https://play.google.com/store/apps/details?id=io.heckel.ntfy))
2. Subscribe to your topic (e.g., "my-llama-servers")
3. Submit jobs with `--ntfy-topic`

Or on desktop:

```bash
# Subscribe via CLI
ntfy subscribe my-llama-servers

# Or via web
open https://ntfy.sh/my-llama-servers
```

**Features:**

- Instant push notifications to phone/desktop
- No authentication required (for public ntfy.sh)
- Includes clickable link to web dashboard
- Works from anywhere (no VPN needed)
- Multiple devices can subscribe to same topic

**Privacy note:** Public ntfy.sh topics are visible to anyone who knows the topic name. Use unique topic names or deploy your own ntfy server for privacy.

## Files

- `submit_llama.sh` - Main script to submit llama.cpp server to SLURM
- `llama_server.slurm` - SLURM batch script that runs llama.cpp server
- `connect_claude_llama.sh` - Connect Claude CLI to running llama.cpp server
- `setup_claude_env.sh` - Environment configuration for Claude CLI
- `connect_openclaw_llama.sh` - Connect OpenClaw CLI to running llama.cpp server
- `setup_openclaw_env.sh` - Environment configuration for OpenClaw
- `register_server.sh` - Auto-register servers with registry (called by SLURM job)
- `send_notification.sh` - Email notification when server is ready
- `send_ntfy_notification.sh` - Push notification via ntfy
- `model_configs/*.conf` - Model configuration files
- `llama_server_connection_<job_id>.txt` - Auto-generated connection info
- `list_servers.sh` - List available servers from registry
- **`registry/`** - Registry server (Flask app, Dockerfile, systemd service, install script)
- **`tests/`** - Test suite (`test_registry.py`, `test_registry.sh`)
- **`docs/`** - Extended documentation

## Requirements

- llama.cpp with server support (`llama-server` binary)
  - **Important**: Must be compiled with CUDA support for your GPU architecture
  - For A100 GPUs (compute capability 8.0): compile with `GGML_CUDA_COMPUTE_CAP=80`
  - For V100 GPUs (compute capability 7.0): compile with `GGML_CUDA_COMPUTE_CAP=70`
  - See [llama.cpp compilation guide](https://github.com/ggml-org/llama.cpp?tab=readme-ov-file#cuda)
- GGUF model files in `~/.cache/llama.cpp/`
- Claude CLI installed
- Python 3 (for port selection and registry server)
- Flask >= 2.0.0 (for registry server — install with `pip install -r requirements.txt`)
- curl (for health checks)
- NVIDIA GPUs with CUDA support
  - **A100 80GB**: Recommended, supports all model configurations
  - **V100 32GB**: Supports qwen3-30b and qwen3-coder-30b (with 4 GPUs)
  - **V100 16GB**: Not recommended for 128K context models

## Troubleshooting

**CUDA error: "no kernel image is available for execution":**

- Your llama-server was compiled for a different GPU architecture
- Check your GPU compute capability: `nvidia-smi --query-gpu=compute_cap --format=csv`
- Recompile llama.cpp with correct architecture:

  ```bash
  cd ~/path/to/llama.cpp
  make clean
  GGML_CUDA=1 GGML_CUDA_COMPUTE_CAP=80 make llama-server  # For A100 (8.0)
  ```

- Or use pre-built binaries matching your GPU

**Server won't start:**

- Check logs: `cat llama_server_<job_id>.log`
- Verify llama-server is installed: `which llama-server`
- Check node resources: `squeue -j <job_id>`
- Verify model file exists: `ls -lh ~/.cache/llama.cpp/`

**Connection issues:**

- Ensure connection file exists: `ls llama_server_connection_*.txt`
- Check server is running: `squeue -j <job_id>`
- Verify network access between nodes

**Model issues:**

- Check available models: `ls ~/.cache/llama.cpp/*.gguf`
- Verify model path in configuration
- Try with different quantization (Q4_K_M, Q8_0, etc.)

**Out of memory errors:**

- Check GPU VRAM: `nvidia-smi`
- GLM-4.7 and qwen3-80b require A100 80GB GPUs
- qwen3-30b models can use V100 32GB with 4 GPUs
- Reduce context size: `--context 65536` (but may impact Claude Code performance)
- Use lower quantization: Q4_K_M instead of Q8_0
