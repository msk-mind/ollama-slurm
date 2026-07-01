#!/usr/bin/env python3

import argparse
import json
from collections import Counter
from datetime import datetime, timezone
from pathlib import Path
import time
from urllib.parse import urlparse, unquote


IGNORE_DIRS = {".git", ".broker", "__pycache__", ".pytest_cache", "node_modules"}


def main():
    parser = argparse.ArgumentParser(description="Repository summary worker")
    parser.add_argument("--job-spec", required=True)
    parser.add_argument("--input-manifest", required=True)
    parser.add_argument("--output-dir", required=True)
    parser.add_argument("--heartbeat-path")
    args = parser.parse_args()

    job_spec = load_json(Path(args.job_spec))
    input_manifest = load_json(Path(args.input_manifest))
    output_dir = Path(args.output_dir)
    output_dir.mkdir(parents=True, exist_ok=True)
    heartbeat_path = Path(args.heartbeat_path) if args.heartbeat_path else output_dir / "heartbeat.json"
    emit_heartbeat(heartbeat_path, job_spec["job_id"], "running", "resolving_inputs", 15, "Preparing repository summary input", {})

    task_params = job_spec.get("task_params", {})
    child_job_ids = task_params.get("child_job_ids") or []
    if child_job_ids:
        return aggregate_child_summaries(job_spec, output_dir, heartbeat_path, task_params, child_job_ids)

    input_refs = input_manifest.get("input_refs", [])
    if not input_refs:
        raise ValueError("repo_summary requires at least one input ref")

    source_path = resolve_file_uri(input_refs[0]["uri"])
    if not source_path.is_dir():
        raise ValueError(f"repo_summary input must be a directory: {source_path}")

    manifest = build_manifest(source_path)
    emit_heartbeat(heartbeat_path, job_spec["job_id"], "running", "preprocessing", 50, "Built repository manifest", {
        "file_count": manifest.get("file_count", 0),
    })
    summary = build_summary(source_path, manifest)
    subsystems = derive_subsystems(source_path, manifest)
    entrypoints = derive_entrypoints(source_path, manifest)
    dependencies = derive_dependencies(source_path)
    risks = derive_risks(source_path, manifest)

    manifest_path = output_dir / "repo_manifest.json"
    write_json(manifest_path, manifest)

    result = {
        "schema_name": job_spec["output_schema"]["name"],
        "schema_version": "1.0.0",
        "payload": {
            "summary": summary,
            "subsystems": subsystems,
            "entrypoints": entrypoints,
            "dependencies": dependencies,
            "risks": risks,
            "evidence_refs": ["artifact_repo_manifest"],
        },
    }

    artifacts = [
        {
            "artifact_id": "artifact_repo_manifest",
            "artifact_type": "chunk_manifest",
            "path": str(manifest_path),
            "classification": input_refs[0].get("classification", "unknown"),
        }
    ]

    emit_heartbeat(heartbeat_path, job_spec["job_id"], "running", "writing_artifacts", 85, "Writing repository summary outputs", {
        "file_count": manifest.get("file_count", 0),
        "subsystem_count": len(subsystems),
    })
    write_json(output_dir / "result.json", result)
    write_json(output_dir / "artifacts.json", artifacts)
    emit_heartbeat(heartbeat_path, job_spec["job_id"], "completed", "completed", 100, "Repository summary ready", {
        "file_count": manifest.get("file_count", 0),
        "subsystem_count": len(subsystems),
    })
    return 0


def aggregate_child_summaries(job_spec, output_dir, heartbeat_path, task_params, child_job_ids):
    run_root = Path(task_params.get("_broker_run_root", ".broker/runs"))
    timeout_seconds = int(task_params.get("aggregate_wait_seconds", 60))
    allow_partial = bool(task_params.get("allow_partial_reduce", True))
    deadline = time.time() + timeout_seconds

    emit_heartbeat(heartbeat_path, job_spec["job_id"], "running", "waiting_for_children", 20, "Waiting for child shard results", {
        "children_total": len(child_job_ids),
    })

    child_results = []
    failed_children = []
    while time.time() < deadline:
        pending, child_results, failed_children = collect_child_outcomes(run_root, child_job_ids)
        if not pending:
            break
        emit_heartbeat(heartbeat_path, job_spec["job_id"], "running", "waiting_for_children", 40, "Reducer is waiting for shard completion", {
            "children_total": len(child_job_ids),
            "children_ready": len(child_results),
            "children_failed": len(failed_children),
            "children_pending": len(pending),
        })
        time.sleep(1)

    pending, child_results, failed_children = collect_child_outcomes(run_root, child_job_ids)
    if pending and not allow_partial:
        raise TimeoutError(f"timed out waiting for child results: ready={len(child_results)} failed={len(failed_children)} total={len(child_job_ids)}")
    if not child_results:
        raise TimeoutError(f"no successful child results were available: failed={len(failed_children)} total={len(child_job_ids)}")

    emit_heartbeat(heartbeat_path, job_spec["job_id"], "running", "aggregating_results", 70, "Aggregating child repository summaries", {
        "children_total": len(child_job_ids),
        "children_succeeded": len(child_results),
        "children_failed": len(failed_children),
    })

    payloads = [result.get("payload", {}) for result in child_results]
    warnings = []
    if failed_children or pending:
        warnings.append("partial_reduce_incomplete_children")
    aggregated = {
        "summary": build_aggregate_summary(payloads),
        "subsystems": dedupe_objects(flatten_lists(payloads, "subsystems"), "name", 10),
        "entrypoints": dedupe_objects(flatten_lists(payloads, "entrypoints"), "path", 12),
        "dependencies": dedupe_objects(flatten_lists(payloads, "dependencies"), "name", 12),
        "risks": dedupe_strings(flatten_lists(payloads, "risks"), 8),
        "evidence_refs": [],
        "warnings": warnings,
        "aggregate_metrics": {
            "children_total": len(child_job_ids),
            "children_succeeded": len(child_results),
            "children_failed": len(failed_children) + len(pending),
            "coverage_fraction": len(child_results) / len(child_job_ids),
        },
    }

    result = {
        "schema_name": job_spec["output_schema"]["name"],
        "schema_version": "1.0.0",
        "payload": aggregated,
    }

    write_json(output_dir / "result.json", result)
    write_json(output_dir / "artifacts.json", [])
    emit_heartbeat(heartbeat_path, job_spec["job_id"], "completed", "completed", 100, "Aggregated repository summary ready", {
        "children_total": len(child_job_ids),
        "children_succeeded": len(child_results),
        "children_failed": len(failed_children) + len(pending),
    })
    return 0


def load_json(path: Path):
    with path.open("r", encoding="utf-8") as handle:
        return json.load(handle)


def write_json(path, payload):
    with path.open("w", encoding="utf-8") as handle:
        json.dump(payload, handle, indent=2)


def emit_heartbeat(path, job_id, state, phase, percent, message, metrics):
    if path is None:
        return
    payload = {
        "job_id": job_id,
        "state": state,
        "phase": phase,
        "percent": percent,
        "message": message,
        "timestamp": datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z"),
        "metrics": metrics,
    }
    write_json(path, payload)


def resolve_file_uri(uri):
    parsed = urlparse(uri)
    if parsed.scheme != "file":
        raise ValueError(f"unsupported input uri: {uri}")
    return Path(unquote(parsed.path))


def build_manifest(root):
    files = []
    language_counts = Counter()
    for path in sorted(root.rglob("*")):
        if should_skip(path, root):
            continue
        if path.is_file():
            rel = path.relative_to(root).as_posix()
            ext = path.suffix.lower()
            language = classify_language(rel, ext)
            files.append({
                "path": rel,
                "size_bytes": path.stat().st_size,
                "language": language,
            })
            language_counts[language] += 1
    return {
        "root": str(root),
        "file_count": len(files),
        "languages": dict(language_counts),
        "files": files[:500],
    }


def should_skip(path, root):
    rel_parts = path.relative_to(root).parts
    return any(part in IGNORE_DIRS for part in rel_parts)


def classify_language(rel, ext):
    if rel == "go.mod" or ext == ".go":
        return "go"
    if ext == ".py":
        return "python"
    if ext in {".md", ".rst", ".txt"}:
        return "docs"
    if ext in {".sh", ".bash"}:
        return "shell"
    if ext in {".json", ".yaml", ".yml", ".toml"}:
        return "config"
    return ext.lstrip(".") or "unknown"


def build_summary(root, manifest):
    languages = manifest.get("languages", {})
    top_languages = sorted(languages.items(), key=lambda item: item[1], reverse=True)[:3]
    lang_text = ", ".join(f"{name} ({count})" for name, count in top_languages) if top_languages else "unknown"
    return (
        f"The repository at {root} contains {manifest['file_count']} tracked files in this summary pass. "
        f"Top content categories: {lang_text}."
    )


def derive_subsystems(root, manifest):
    top_dirs = Counter()
    for file_info in manifest["files"]:
        parts = file_info["path"].split("/")
        top_dirs[parts[0] if len(parts) > 1 else "."] += 1

    subsystems = []
    for name, count in top_dirs.most_common(5):
        role = infer_role(name)
        path = f"{name}/" if name != "." else "."
        subsystems.append({
            "name": name,
            "role": role,
            "paths": [path],
            "confidence": 0.7,
            "file_count": count,
        })
    return subsystems


def infer_role(name):
    lower = name.lower()
    if "broker" in lower:
        return "control plane"
    if "worker" in lower:
        return "worker runtime"
    if "deploy" in lower:
        return "deployment assets"
    if "config" in lower:
        return "configuration"
    if "test" in lower:
        return "tests"
    return "repository subsystem"


def derive_entrypoints(root, manifest):
    entrypoints = []
    for file_info in manifest["files"]:
        path = file_info["path"]
        if path.endswith("/main.go") or path.endswith("/main.py") or path in {"go.mod", "README.md"}:
            entrypoints.append({
                "path": path,
                "kind": classify_entrypoint(path),
            })
        if len(entrypoints) == 10:
            break
    return entrypoints


def classify_entrypoint(path):
    if path.endswith("/main.go") or path.endswith("/main.py"):
        return "service_entrypoint"
    if path == "go.mod":
        return "module_root"
    return "project_entrypoint"


def derive_dependencies(root):
    deps = []
    go_mod = root / "go.mod"
    if go_mod.exists():
        deps.append({"name": "Go toolchain", "kind": "build_dependency"})
    if any(root.rglob("*.py")):
        deps.append({"name": "Python 3", "kind": "runtime_dependency"})
    if (root / "deploy" / "slurm").exists():
        deps.append({"name": "Slurm", "kind": "runtime_dependency"})
    return deps


def derive_risks(root, manifest):
    risks = []
    if not (root / "broker").exists():
        risks.append("Repository does not yet contain a dedicated broker package.")
    if manifest["file_count"] == 0:
        risks.append("No files were discovered in the repository summary pass.")
    if (root / "tests").exists() is False:
        risks.append("No tests directory was found.")
    if not risks:
        risks.append("Repository structure appears consistent with the current summary heuristics.")
    return risks[:5]


def build_aggregate_summary(payloads):
    summaries = [payload.get("summary", "").strip() for payload in payloads if payload.get("summary")]
    count = len(payloads)
    if not summaries:
        return f"Aggregated {count} repository shard summaries."
    lead = summaries[0][:240]
    return f"Aggregated {count} repository shard summaries. Lead shard summary: {lead}"


def flatten_lists(payloads, key):
    items = []
    for payload in payloads:
        value = payload.get(key, [])
        if isinstance(value, list):
            items.extend(value)
    return items


def dedupe_objects(items, key, limit):
    seen = set()
    output = []
    for item in items:
        if not isinstance(item, dict):
            continue
        value = item.get(key)
        if value in seen:
            continue
        seen.add(value)
        output.append(item)
        if len(output) == limit:
            break
    return output


def dedupe_strings(items, limit):
    seen = set()
    output = []
    for item in items:
        if not isinstance(item, str):
            continue
        if item in seen:
            continue
        seen.add(item)
        output.append(item)
        if len(output) == limit:
            break
    return output


def collect_child_outcomes(run_root, child_job_ids):
    pending = []
    results = []
    failed = []
    for job_id in child_job_ids:
        run_dir = run_root / job_id
        result_path = run_dir / "result.json"
        if result_path.exists():
            payload = try_load_json(result_path)
            if payload is None:
                pending.append(job_id)
                continue
            results.append(payload)
            continue
        if child_failed(run_dir):
            failed.append(job_id)
            continue
        pending.append(job_id)
    return pending, results, failed


def child_failed(run_dir):
    heartbeat_path = run_dir / "heartbeat.json"
    if heartbeat_path.exists():
        heartbeat = try_load_json(heartbeat_path)
        if heartbeat is not None and str(heartbeat.get("state", "")).lower() == "failed":
            return True
    metadata_path = run_dir / "run-metadata.json"
    if metadata_path.exists():
        metadata = try_load_json(metadata_path)
        if metadata is not None and str(metadata.get("status", "")).lower() == "failed":
            return True
    return False


def try_load_json(path: Path):
    try:
        return load_json(path)
    except (json.JSONDecodeError, OSError):
        return None


if __name__ == "__main__":
    raise SystemExit(main())
