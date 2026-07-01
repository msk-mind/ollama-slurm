# Tests

Test code should be split by scope:

- `unit/`
- `integration/`
- `e2e/`

The first end-to-end path should cover:

- MCP submission
- Slurm-backed execution
- result retrieval
- cache hit on repeated request
- safe-summary release behavior

Current smoke scripts:

- `tests/e2e/smoke_command_mode.sh`: fake-Slurm document-summary control-plane smoke
- `tests/e2e/smoke_rag_llamacpp_runtime.sh`: local-backend RAG smoke with a fake OpenAI-compatible `llama.cpp` endpoint
- `tests/e2e/smoke_rag_llamacpp_unavailable.sh`: local-backend RAG smoke with an unreachable configured `llama.cpp` endpoint
- `tests/e2e/smoke_rag_no_real_backend.sh`: local-backend RAG smoke with no configured live local runtime, asserting `execution_quality=no_real_backend`
- `tests/e2e/run_smoke_suite.sh`: runs the default smoke set and optionally the loopback-binding RAG runtime smoke via `--with-loopback-bind`

Suggested usage:

```bash
bash tests/e2e/run_smoke_suite.sh
bash tests/e2e/run_smoke_suite.sh --with-loopback-bind
/usr/bin/env OLLAMA_SLURM_E2E_LOOPBACK=1 /usr/bin/go test ./tests/e2e -run TestLocalBackendRAGLlamaCPPRuntimeSmoke -count=1
```

Current unit coverage should also include:

- Codex-to-llama.cpp compatibility catalog normalization
- Codex-to-llama.cpp responses tool rewriting
