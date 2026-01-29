# llama.cpp SLURM Integration for Claude CLI

Scripts to run llama.cpp server on SLURM and connect Claude CLI to it.

## Quick Start

1. **Submit llama.cpp server with a saved configuration:**
   ```bash
   ./submit_llama.sh --config qwen3-30b
   ```

2. **Wait for server to start (check logs):**
   ```bash
   tail -f llama_server_<job_id>.log
   ```

3. **Connect Claude CLI to the server:**
   ```bash
   ./connect_claude.sh <job_id>
   ```

## Usage

### Submit llama.cpp Server

```bash
./submit_llama.sh [OPTIONS]
```

**Options:**
- `--config NAME` - Use predefined model config
- `--list-configs` - List available model configurations
- `--model FILE` - Path to GGUF model file
- `--time TIME` - Time limit (default: 4:00:00)
- `--no-time-limit` - Remove time limit entirely
- `--partition PART` - SLURM partition (default: none)
- `--qos QOS` - SLURM QOS (default: none)
- `--cpus N` - Number of CPUs (default: 8)
- `--mem SIZE` - Memory allocation (default: 32G)
- `--gpus N` - Number of GPUs (default: 1)
- `--context N` - Context size (default: 131072)
- `--gpu-layers N` - GPU layers, -1 for all (default: -1)
- `--extra-args STR` - Additional llama-server arguments
- `--help, -h` - Show help message

**Examples:**
```bash
# Use saved configuration
./submit_llama.sh --config qwen3-30b

# List available configurations
./submit_llama.sh --list-configs

# No time limit with saved config
./submit_llama.sh --config glm-4.7 --no-time-limit

# Custom model file
./submit_llama.sh --model ~/.cache/llama.cpp/model.gguf

# Custom settings
./submit_llama.sh --model model.gguf --gpus 2 --context 16384
```

### Model Configurations

Model configurations are stored in `model_configs/*.conf`. Each config sets optimal parameters based on [claude-code-tools recommendations](https://github.com/pchalasani/claude-code-tools/blob/main/docs/local-llm-setup.md).

**Server settings (all models):**
- Context: 131072 tokens (128K)
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

| Config | Model | GPUs | Memory | GPU Requirements | Notes |
|--------|-------|------|--------|------------------|-------|
| `qwen3-30b` | Qwen3 30B Q4_K_M | 2 | 64G | 2x A100 80GB or 4x V100 32GB | ~18GB model + 13GB KV cache/GPU |
| `qwen3-coder-30b` | Qwen3 Coder 30B Q8_0 | 2 | 64G | 2x A100 80GB or 4x V100 32GB | ~32GB model + 13GB KV cache/GPU |
| `qwen3-80b` | Qwen3 80B Q4_K_XL | 4 | 128G | 4x A100 80GB only | ~47GB model + 13GB KV cache/GPU |
| `glm-4.7` | GLM-4.7 Flash Q4_K_M | 2 | 32G | 2x A100 80GB only | ~16GB model + 13GB KV cache/GPU, DeepSeek2 gating |

**GPU VRAM Notes:**
- **A100 80GB**: Can run all configurations
- **V100 32GB**: Can run qwen3-30b and qwen3-coder-30b with 4 GPUs, NOT glm-4.7 or qwen3-80b
- **V100 16GB**: Not recommended for these models with 128K context
- KV cache at 128K context requires ~13GB VRAM per GPU for all models

### Connect Claude CLI

```bash
./connect_claude.sh <job_id> [working_directory]
```

**Arguments:**
- `job_id` - SLURM job ID of the running llama.cpp server
- `working_directory` - Optional directory where Claude should run (default: current directory)

**Examples:**
```bash
# Connect from current directory
./connect_claude.sh 2883398

# Connect and work in a specific directory
./connect_claude.sh 2883398 /path/to/my/project

# Pass additional arguments to Claude
./connect_claude.sh 2883398 ~/code --resume abc123
```

**Note**: On first connection to a directory, Claude will ask you to confirm trust of the workspace. Make sure to run `connect_claude.sh` from an interactive terminal (not a script). If you see the "trust this folder" prompt, select "Yes" to continue. You can also pre-approve with: `claude trust /path/to/workspace`

Lists active llama.cpp server jobs if no job ID is provided.

### Manual Connection

If you prefer to connect manually:

```bash
source llama_server_connection_<job_id>.txt
source setup_claude_env.sh
claude
```

## Managing Jobs

**Check job status:**
```bash
squeue -u $USER -n llama-server
```

**View logs:**
```bash
tail -f llama_server_<job_id>.log
```

**Cancel job:**
```bash
scancel <job_id>
```

## Files

- `submit_llama.sh` - Main script to submit llama.cpp server to SLURM
- `llama_server.slurm` - SLURM batch script that runs llama.cpp server
- `connect_claude.sh` - Connect Claude CLI to running llama.cpp server
- `setup_claude_env.sh` - Environment configuration for Claude CLI
- `model_configs/*.conf` - Model configuration files
- `llama_server_connection_<job_id>.txt` - Auto-generated connection info

## Requirements

- llama.cpp with server support (`llama-server` binary)
  - **Important**: Must be compiled with CUDA support for your GPU architecture
  - For A100 GPUs (compute capability 8.0): compile with `GGML_CUDA_COMPUTE_CAP=80`
  - For V100 GPUs (compute capability 7.0): compile with `GGML_CUDA_COMPUTE_CAP=70`
  - See [llama.cpp compilation guide](https://github.com/ggml-org/llama.cpp?tab=readme-ov-file#cuda)
- GGUF model files in `~/.cache/llama.cpp/`
- Claude CLI installed
- Python 3 (for port selection)
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
