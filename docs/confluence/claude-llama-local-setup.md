# Using Claude CLI with Local Llama Models on MSK HPC Cluster

## Overview

This guide explains how to use the Claude CLI with local Llama models using llama.cpp server integration with SLURM job scheduling on the MSK HPC cluster. This allows you to run large language models locally on our organization's compute infrastructure while leveraging the cluster's optimized resources.

## Prerequisites

- Access to the MSK HPC cluster with SLURM job scheduler
- CUDA-capable GPUs (A100 80GB recommended for best performance)
- llama.cpp compiled with CUDA support
- GGUF model files in `~/.cache/llama.cpp/`
- Claude CLI installed

## Key Components

1. `submit_llama.sh` - Submit llama.cpp server jobs to SLURM
2. `connect_claude_llama.sh` - Connect Claude CLI to running server
3. `llama_server.slurm` - SLURM batch script that runs llama.cpp server
4. Model configuration files in `model_configs/`

## Submitting llama.cpp Servers

The `submit_llama.sh` script submits a llama.cpp server to SLURM with optimized settings for local LLM inference.

### Quick Start

1. Submit a pre-configured model:
   ```bash
   ./submit_llama.sh --config qwen3-30b
   ```

2. Wait for server to start:
   ```bash
   tail -f llama_server_<job_id>.log
   ```

3. Connect Claude CLI:
   ```bash
   ./connect_claude_llama.sh <job_id>
   ```

### Available Model Configurations

| Config | Model | GPUs | Memory | GPU Requirements | Notes |
|--------|-------|------|--------|------------------|-------|
| `qwen3-30b` | Qwen3 30B Q4_K_M | 2 | 64G | 2x A100 80GB or 4x V100 32GB | ~18GB model + 13GB KV cache/GPU |
| `qwen3-coder-30b` | Qwen3 Coder 30B Q8_0 | 2 | 64G | 2x A100 80GB or 4x V100 32GB | ~32GB model + 13GB KV cache/GPU |
| `qwen3-80b` | Qwen3 80B Q4_K_XL | 4 | 128G | 4x A100 80GB only | ~47GB model + 13GB KV cache/GPU |
| `glm-4.7` | GLM-4.7 Flash Q4_K_M | 2 | 32G | 2x A100 80GB only | ~16GB model + 13GB KV cache/GPU, DeepSeek2 gating |

### Model Configuration Files

Model configurations are stored in `model_configs/*.conf`. Each config contains:
- Model file path
- CPU allocation
- Memory allocation
- GPU count
- Context size
- GPU layer settings
- Extra arguments

### Advanced Options

```bash
# Submit with custom settings
./submit_llama.sh --config qwen3-30b --gpus 2 --context 65536 --mem 64G

# Submit with email notifications
./submit_llama.sh --config qwen3-30b --email user@example.com

# Submit with ntfy push notifications
./submit_llama.sh --config qwen3-30b --ntfy-topic my-llama-servers
```

### Troubleshooting

**CUDA error:** If you get "no kernel image is available for execution", your llama-server was compiled for a different GPU architecture. Check compute capabilities with `nvidia-smi --query-gpu=compute_cap --format=csv` and recompile with correct architecture.

**Server won't start:** Check logs with `cat llama_server_<job_id>.log` or check if the server is running with `squeue -j <job_id>`.

## Connecting Claude CLI to Local Servers

The `connect_claude_llama.sh` script sets up the proper environment to connect Claude CLI to a running llama.cpp server.

### Quick Connection

```bash
# Connect to a running server
./connect_claude_llama.sh <slurm_job_id>

# Connect from a specific working directory
./connect_claude_llama.sh <slurm_job_id> /path/to/my/project
```

### Environment Setup

The connection process automatically:
1. Validates that the SLURM job exists and is running
2. Sources the connection information file (`llama_server_connection_<job_id>.txt`)
3. Sets up environment variables for Claude CLI:
   - `ANTHROPIC_BASE_URL` - Points to the local llama.cpp server
   - `ANTHROPIC_AUTH_TOKEN=ollama` - Required for local server connection
   - Disables telemetry and error reporting for local usage

### Manual Connection

For advanced usage, you can manually connect:

```bash
# Source connection info
source llama_server_connection_<job_id>.txt

# Set up Claude environment
source setup_claude_env.sh

# Launch Claude CLI
claude
```

### Working Directory Issues

When connecting to Claude, some directories may cause it to hang due to a known issue with Claude Code. If this occurs:

1. Try connecting from a different directory
2. Start Claude in a working directory, then access problematic directory files as needed
3. For directories with known issues, use Claude with the official Anthropic API instead

### Command-line Arguments

The `connect_claude_llama.sh` script accepts additional arguments to pass to Claude:

```bash
./connect_claude_llama.sh 2883398 ~/code --resume abc123
```

### Troubleshooting

**Workspace trust prompt:** On first connection to a directory, Claude will ask you to confirm trust of the workspace. This prompt must be answered interactively. Pre-approve with: `claude trust /path/to/workspace`

**Connection timeout:** If Claude takes too long to initialize, wait longer or try a different working directory.

## SLURM Integration Details

### SLURM Batch Script

The `llama_server.slurm` script defines how llama.cpp servers are run on the cluster:

- Automatically detects available CUDA toolkit
- Finds an open port for the server
- Creates connection information files
- Runs llama.cpp server with optimized parameters:
  - Context size: 131072 tokens (128K)
  - Batch size: 32768
  - Ubatch: 1024
  - Parallel slots: 1
  - Jinja templating: enabled

### Server Registration and Discovery

The system includes a central registry for discovering servers without shared filesystem access:

1. Servers automatically register with the registry upon startup
2. Servers unregister upon shutdown
3. Users can discover servers via API: `curl http://your-registry-server:5000/servers | jq`

To enable registry discovery, set:
```bash
export REGISTRY_URL="http://your-registry-server:5000"
```

### Notification Systems

The system supports two notification systems:

1. **Email Notifications**: Receive emails when servers are ready
2. **ntfy Push Notifications**: Get instant push notifications to mobile/desktop

**Email Setup Requirements:**
- System must have `mail`, `sendmail`, or `mutt` installed
- SMTP service configured on compute nodes or login nodes

**ntfy Setup:**
- Install ntfy app on your phone or desktop
- Subscribe to topics to receive notifications
- Use public ntfy.sh or your own instance for privacy

## Requirements and System Requirements

### Hardware Requirements

- **NVIDIA GPUs with CUDA support**
  - **A100 80GB**: Recommended, supports all model configurations
  - **V100 32GB**: Supports qwen3-30b and qwen3-coder-30b (with 4 GPUs)
  - **V100 16GB**: Not recommended for 128K context models

### Software Requirements

- **llama.cpp with server support** (`llama-server` binary)
  - Must be compiled with CUDA support for your GPU architecture
  - For A100 GPUs (compute capability 8.0): compile with `GGML_CUDA_COMPUTE_CAP=80`
  - For V100 GPUs (compute capability 7.0): compile with `GGML_CUDA_COMPUTE_CAP=70`
- GGUF model files in `~/.cache/llama.cpp/`
- Claude CLI installed
- Python 3 (for port selection)
- curl (for health checks)
- SLURM job scheduler

### Model File Requirements

Model files must be in GGUF format and located in `~/.cache/llama.cpp/`. Pre-trained models can be downloaded from HuggingFace or other sources and converted using llama.cpp tools.

### Troubleshooting Common Issues

1. **CUDA error: "no kernel image is available for execution"**
   - Your llama-server was compiled for a different GPU architecture
   - Check your GPU compute capability: `nvidia-smi --query-gpu=compute_cap --format=csv`
   - Recompile llama.cpp with correct architecture

2. **Server won't start**
   - Check logs: `cat llama_server_<job_id>.log`
   - Verify llama-server is installed: `which llama-server`
   - Check node resources: `squeue -j <job_id>`
   - Verify model file exists: `ls -lh ~/.cache/llama.cpp/`

3. **Connection issues**
   - Ensure connection file exists: `ls llama_server_connection_*.txt`
   - Check server is running: `squeue -j <job_id>`
   - Verify network access between nodes

4. **Out of memory errors**
   - Check GPU VRAM: `nvidia-smi`
   - GLM-4.7 and qwen3-80b require A100 80GB GPUs
   - qwen3-30b models can use V100 32GB with 4 GPUs
   - Reduce context size: `--context 65536` (but may impact Claude Code performance)
   - Use lower quantization: Q4_K_M instead of Q8_0

## Tips and Tricks for Claude CLI

Based on best practices from the Claude community, here are some helpful tips and tricks for using Claude CLI with local LLMs:

### Environment Configuration
- Set `DISABLE_TELEMETRY=1` and `DISABLE_ERROR_REPORTING=1` to reduce network traffic when working with local servers
- Use `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1` to disable unnecessary network requests that can slow down local operations

### Performance Optimization
- For better performance with large context models, consider using `--context 65536` instead of the default 131072 when you don't need the full context window
- Use lower quantization models (like Q4_K_M) when you're not seeing significant quality differences but want faster inference
- When using Claude in a directory, you can pre-approve workspace trust with `claude trust /path/to/workspace` to avoid interactive prompts

### Workflow Efficiency
- Create aliases or shell scripts for frequently used commands to speed up your workflow
- Use `--ntfy-topic` and `--email` flags when submitting jobs so you're notified when servers are ready
- Monitor GPU usage with `nvidia-smi` to ensure your local models aren't consuming more resources than expected

### Debugging
- When Claude hangs or takes a long time to respond, try changing your working directory to avoid workspace trust prompts
- Use `tail -f llama_server_<job_id>.log` to monitor server startup progress
- If you see connection errors, verify that your server is running and listening on the correct port with `netstat -tulpn | grep <port>`

**Sources:**
- [LLaMA.cpp GitHub Repository](https://github.com/ggml-org/llama.cpp)
- [Claude Code Tools Documentation](https://github.com/pchalasani/claude-code-tools/blob/main/docs/local-llm-setup.md)

**Note:** This system was designed to work with the SLURM job scheduler for HPC clusters. Users without SLURM access or shared filesystems can use alternative connection methods, but these are outside the scope of this documentation.

**Version:** 1.0
**Last Updated:** 2026-02-05

**For any questions, contact your HPC administrator or refer to the project GitHub repository.**

**Related Documentation:**
- [Registry Setup Guide](REGISTRY_SETUP.md)
- [Model Configuration Guide](MODEL_CONFIGS.md)

**Confluence Page URL:** https://mskconfluence.mskcc.org/spaces/CDSI/pages/113570942/User+Docs

**Created by:** Claude Code Assistant