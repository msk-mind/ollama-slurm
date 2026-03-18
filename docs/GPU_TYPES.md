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

### Claude CLI / Claude Code Context Requirements

Claude CLI requires at least **32K context** to function, but **128K is strongly recommended**:

| Context | Claude CLI Usability |
|---------|----------------------|
| 128K    | ✅ Full functionality — recommended for all use cases |
| 64K     | ⚠️ Functional minimum — large files and long conversations may hit limits |
| 32K     | ⚠️ Marginal — short tasks only; reasoning models may exhaust context quickly |
| 16K     | ❌ Too small for practical Claude CLI use |

V100 and P40 profiles with reduced context are fallbacks when A100s are unavailable, not recommended for demanding Claude CLI workloads.

## Model Configs

Each model has separate config files per GPU type: `model_configs/<model>.<gpu_type>.conf`

### Available Profiles

| Config | GPUs | Context | VRAM/GPU | Notes |
|--------|------|---------|----------|-------|
| **qwen3-30b.a100** | 1x A100 | 128K | ~44GB | Full context, single GPU |
| **qwen3-30b.v100** | 4x V100 | 64K | ~7.75GB | ⚠️ Reduced context — Claude CLI functional minimum |
| **qwen3-30b.p40** | 1x P40 | 16K | ~21.25GB | ❌ Context too small for Claude CLI |
| **qwen3-coder-30b.a100** | 1x A100 | 128K | ~58GB | Q8_0 quant, single GPU |
| **qwen3-coder-30b.v100** | 4x V100 | 32K | ~9.6GB | ⚠️ Marginal context — short tasks only |
| **qwen3-next-80b.a100** | 4x A100 | 128K | ~24.75GB | Instruct variant (no reasoning traces) |
| **qwen3-next-80b.a100.thinking** | 4x A100 | 128K | ~24.75GB | Thinking variant — emits `<think>` traces; lower temp, larger output budget |
| **glm-4.7.a100** | 2x A100 | 128K | — | DeepSeek2 MLA + 64 experts, needs >80GB |
| **glm-z1-32b.a100** | 1x A100 | 128K | ~45GB | Dense 32B reasoning model, emits `<think>` traces |
| **glm-z1-32b.v100** | 4x V100 | 32K | ~6.4GB | ⚠️ Native 32K context limit — marginal for Claude CLI |

## Usage

```bash
# A100 profiles (full context — recommended for Claude CLI)
./submit_llama.sh --config qwen3-30b.a100
./submit_llama.sh --config qwen3-coder-30b.a100
./submit_llama.sh --config glm-4.7.a100
./submit_llama.sh --config qwen3-next-80b.a100
./submit_llama.sh --config qwen3-next-80b.a100.thinking  # reasoning traces enabled
./submit_llama.sh --config glm-z1-32b.a100

# V100 profiles (reduced context — fallback when A100s unavailable)
./submit_llama.sh --config qwen3-30b.v100
./submit_llama.sh --config qwen3-coder-30b.v100
./submit_llama.sh --config glm-z1-32b.v100

# P40 profiles (limited context — not recommended for Claude CLI)
./submit_llama.sh --config qwen3-30b.p40
```

## Why Some Models Don't Have V100/P40 Profiles

- **qwen3-next-80b**: 47GB model exceeds 64GB total of 4x V100 with any usable context
- **qwen3-coder-30b on P40**: 32GB Q8_0 model exceeds 24GB P40 VRAM
- **qwen3-80b on P40**: 47GB model far exceeds 24GB P40 VRAM
- **glm-4.7 on V100/P40**: DeepSeek2 MLA architecture with 64 experts needs >80GB VRAM; reduced context on V100/P40 is too small for Claude CLI
- **glm-z1-32b on P40**: ~19GB model requires tensor parallelism across GPUs, not available with a single P40
- **Qwen3-235B-A22B**: ~142GB Q4_K_M — too large for current cluster (4x A100 = 320GB leaves insufficient headroom for 128K KV cache)
