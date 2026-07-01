# broker-cli

Minimal command-line client for the broker HTTP API.

Current commands:

- `submit`
- `get`
- `root`
- `watch`
- `result`
- `cancel`
- `verify-audit`
- `rotate-audit`
- `prune-audit`

This is intended as a lightweight demo and operator surface while MCP integration and richer client examples are still being built.

`watch` polls the broker and prints compact progress updates, including worker phase and percent when `heartbeat.json` is present in the run directory.

Example:

```bash
broker-cli submit \
  --task-type log_analysis \
  --input-uri file:///tmp/build.log \
  --schema log_analysis_v1

broker-cli watch job_1234abcd
broker-cli root root_abcd1234
```

Audit verification:

```bash
broker-cli verify-audit --path .broker/audit.jsonl
```

This validates the hash chain across the JSONL audit file and exits non-zero if a record has been modified, deleted, or reordered.

Audit rotation:

```bash
broker-cli rotate-audit --path .broker/audit.jsonl
```

This archives the current audit file, stores the last hash in a sidecar state file, and lets the next active segment continue the chain.

Audit pruning:

```bash
broker-cli prune-audit --path .broker/audit.jsonl --keep 10
```

This deletes older rotated audit segments and retains only the newest `--keep` archives. The active file and chain seed state are preserved.
