# Parallel Execution

## Purpose

The broker should support one logical investigation faning out into many local worker jobs and then collapsing back into one compact result.

## Execution Modes

- independent parallel jobs
- parent/child shard jobs
- aggregator or reducer jobs

## Orchestration Metadata

Jobs may carry orchestration metadata:

```json
{
  "orchestration": {
    "parent_job_id": "job_parent_01",
    "root_job_id": "job_root_01",
    "strategy": "fanout_child",
    "shard_key": "repo:src",
    "shard_index": 3,
    "shard_count": 12,
    "aggregation_key": "repo-summary-pass-1",
    "depends_on_job_ids": ["job_extract_01"]
  }
}
```

Recommended strategies:

- `standalone`
- `fanout_child`
- `aggregator`
- `reducer`
- `validator`

## Broker Semantics

Each child job is still a normal broker job:

- its own cache key
- its own backend run
- its own retry behavior
- its own result schema

Shared investigation identity comes from:

- `root_job_id`
- `parent_job_id`
- shard and aggregation metadata

## Slurm Mapping

For the Slurm backend, parallelism can come from:

- one `sbatch` per child
- one `sbatch --array` submission for homogeneous uncached child shards
- native dependency flags for reducers

The broker contract should not depend on Slurm arrays. Arrays are an optimization only.
Very large shard sets should be chunked into multiple bounded backend submissions rather than one oversized array.
When root-level throttling is enabled, later chunks may remain broker-visible in `dispatching` state until earlier chunks drain and a submission slot opens.

## Current Status

Implemented now:

- orchestration metadata in broker request and job records
- capability advertisement for client-orchestrated fan-out
- broker-side `submit_parallel_jobs` helper for child batch submission
- optional reducer job submission after child fan-out
- root investigation status view by `root_job_id`
- root status tracks effective latest shard and reducer attempts rather than counting stale failed attempts forever
- concrete reducer workers for `repo_summary` and `log_analysis`
- Slurm reducer submissions use native `sbatch --dependency=afterany:...`
- Slurm command-mode backends can batch homogeneous uncached child shards into one job array while preserving one broker job record per shard
- broker-side backpressure can chunk large uncached shard sets into multiple bounded backend batches via `BROKER_PARALLEL_MAX_BATCH_SIZE`
- broker-side root throttling can limit how many child batches are actively submitted at once via `BROKER_PARALLEL_MAX_ACTIVE_BATCHES`
- root status exposes throttling telemetry such as active chunks, pending chunks, and deferred reducer state
- broker-side failed-shard retry can resubmit only the currently failed shard attempts and optionally submit a fresh reducer
- reducers can return bounded partial aggregates with coverage metrics when some children fail
- multiple parallel jobs across both Slurm and local backends

Not implemented yet:

- server-side DAG scheduler
- shard-aware cache dedup across composite jobs
