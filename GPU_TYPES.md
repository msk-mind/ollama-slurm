# GPU Configuration Guide

## Available GPU Types

The cluster has three GPU types available with different memory and availability:

| GPU Type | Memory | Max per Node | Best For |
|----------|--------|--------------|----------|
| **A100** | 80 GB  | 4            | Large models, high memory requirements (preferred) |
| **V100** | 16 GB  | 4            | Smaller models, can use multiple GPUs |
| **P40**  | 23 GB  | 1            | Small-medium single-GPU models only |

## GPU Selection Strategy

- **A100s are preferred** for most workloads due to their large 80GB memory
- **P40s are limited to 1 per node** - only suitable for small models that fit in 23GB
- **V100s can be used for multi-GPU jobs** when A100s are unavailable, but have only 16GB each

## Using GPU Types

### In Model Configs

All model configs now include a `GPU_TYPE` parameter:

```bash
GPU_TYPE="a100"  # a100 (80GB), v100 (16GB), or p40 (23GB, max 1/node)
```

### Command Line Override

You can override the GPU type from the config:

```bash
./submit_llama.sh --config qwen3-30b --gpu-type v100 --gpus 4
```

Or specify when not using a config:

```bash
./submit_llama.sh --model model.gguf --gpu-type a100 --gpus 2
```

### For Ollama

```bash
./submit_ollama.sh --model llama3.2 --gpu-type a100 --gpus 1
```

## Model Requirements

### Current Model Configs

| Model | GPUs | GPU Type | Memory Needed | Notes |
|-------|------|----------|---------------|-------|
| **qwen3-30b** | 2 | a100 | ~31GB/GPU | Can use 4x v100 but not recommended |
| **qwen3-80b** | 4 | a100 | ~60GB/GPU | A100 only - won't fit on v100/p40 |
| **qwen3-coder-30b** | 2 | a100 | ~45GB/GPU | Q8_0 quant, A100 only |
| **glm-4.7** | 2 | a100 | ~29GB/GPU | Needs 2 GPUs, p40 limited to 1/node |

### P40 Limitations

Since P40 is limited to 1 GPU per node with 23GB, it's only suitable for:
- Small models (<20GB total with KV cache)
- Single GPU inference
- Not recommended for multi-GPU models in the current configs

## Examples

```bash
# Use config with default A100s
./submit_llama.sh --config qwen3-30b

# Override to use V100s (needs 4 instead of 2 A100s)
./submit_llama.sh --config qwen3-30b --gpu-type v100 --gpus 4

# Single P40 for small model
./submit_llama.sh --model small-model.gguf --gpu-type p40 --gpus 1

# Explicitly request A100s
./submit_llama.sh --config qwen3-80b --gpu-type a100
```
