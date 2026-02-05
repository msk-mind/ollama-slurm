# GPU Configuration Guide

## Available GPU Types

The cluster has three GPU types available with different memory and availability:

| GPU Type | Memory | Max per Node | Best For |
|----------|--------|--------------|----------|
| **A100** | 80 GB  | 4            | Large models, high memory requirements (preferred) |
| **V100** | 16 GB  | 4            | Smaller models with reduced context, multi-GPU splits |
| **P40**  | 24 GB  | 1            | Small single-GPU models with limited context |

## GPU Selection Strategy

- **A100s are preferred** for most workloads due to their large 80GB memory
- **V100s can be used for multi-GPU jobs** when A100s are unavailable, but context size must be reduced
- **P40s are limited to 1 per node** - only suitable for models that fit in 24GB with small context windows

## Model Configs

Each model has separate config files per GPU type: `model_configs/<model>.<gpu_type>.conf`

### Available Profiles

| Config | GPUs | Context | VRAM/GPU | Notes |
|--------|------|---------|----------|-------|
| **qwen3-30b.a100** | 1x A100 | 128K | ~44GB | Full context, single GPU |
| **qwen3-30b.v100** | 4x V100 | 64K | ~7.75GB | Reduced context |
| **qwen3-30b.p40** | 1x P40 | 16K | ~21.25GB | Limited context |
| **qwen3-80b.a100** | 4x A100 | 128K | ~24.75GB | A100 only - too large for V100/P40 |
| **qwen3-coder-30b.a100** | 1x A100 | 128K | ~58GB | Q8_0 quant, single GPU |
| **qwen3-coder-30b.v100** | 4x V100 | 32K | ~9.6GB | Reduced context, P40 too small |
| **glm-4.7.a100** | 2x A100 | 128K | -- | DeepSeek2 MLA + 64 experts, needs >80GB |

## Usage

```bash
# A100 profiles (full context)
./submit_llama.sh --config qwen3-30b.a100
./submit_llama.sh --config qwen3-80b.a100
./submit_llama.sh --config qwen3-coder-30b.a100
./submit_llama.sh --config glm-4.7.a100

# V100 profiles (reduced context)
./submit_llama.sh --config qwen3-30b.v100
./submit_llama.sh --config qwen3-coder-30b.v100
# P40 profiles (limited context)
./submit_llama.sh --config qwen3-30b.p40
```

## Why Some Models Don't Have V100/P40 Profiles

- **qwen3-80b**: 47GB model exceeds 64GB total of 4x V100 with any usable context
- **qwen3-coder-30b on P40**: 32GB Q8_0 model exceeds 24GB P40 VRAM
- **qwen3-80b on P40**: 47GB model far exceeds 24GB P40 VRAM
- **glm-4.7 on V100/P40**: DeepSeek2 MLA architecture with 64 experts needs >80GB VRAM; reduced context on V100/P40 is too small for Claude CLI
