#!/usr/bin/env python3

import argparse
import json
from datetime import datetime, timezone
from pathlib import Path
import time
from urllib.parse import urlparse, unquote


ERROR_PATTERNS = [
    ("MISSING_GENERATED_HEADER", "fatal error:", "high", [
        "Run the code generation step before compiling.",
        "Verify generated include paths are available to the compiler.",
    ]),
    ("UNDEFINED_REFERENCE", "undefined reference", "high", [
        "Check linker inputs and library ordering.",
        "Confirm the missing symbol is built for the current target.",
    ]),
    ("MODULE_NOT_FOUND", "ModuleNotFoundError", "medium", [
        "Install the missing dependency in the runtime environment.",
        "Verify the correct Python environment is active.",
    ]),
    ("TEST_FAILURE", "FAILED", "medium", [
        "Inspect the first failing test and its setup path.",
        "Compare the failing environment with a known-good run.",
    ]),
]


def main():
    parser = argparse.ArgumentParser(description="Log analysis worker")
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
    emit_heartbeat(heartbeat_path, job_spec["job_id"], "running", "preprocessing", 20, "Loading log input", {})

    task_params = job_spec.get("task_params", {})
    child_job_ids = task_params.get("child_job_ids") or []
    if child_job_ids:
        return aggregate_child_results(job_spec, output_dir, heartbeat_path, task_params, child_job_ids)

    input_refs = input_manifest.get("input_refs", [])
    if not input_refs:
        raise ValueError("log_analysis requires at least one input ref")

    source_path = resolve_file_uri(input_refs[0]["uri"])
    text = source_path.read_text(encoding="utf-8", errors="replace")
    lines = text.splitlines()
    emit_heartbeat(heartbeat_path, job_spec["job_id"], "running", "running_model", 60, "Classifying log failures", {
        "line_count": len(lines),
    })

    findings = derive_findings(lines)
    summary = build_summary(lines, findings)
    timeline = build_timeline(lines)
    suggested_next_steps = derive_next_steps(findings)

    artifacts = []
    if findings:
        excerpt_path = output_dir / "log_excerpt.txt"
        excerpt_path.write_text(build_excerpt(lines), encoding="utf-8")
        artifacts.append({
            "artifact_id": "artifact_log_excerpt",
            "artifact_type": "redacted_excerpt",
            "path": str(excerpt_path),
            "classification": input_refs[0].get("classification", "unknown"),
        })
        for finding in findings:
            finding["evidence_refs"] = ["artifact_log_excerpt"]
    emit_heartbeat(heartbeat_path, job_spec["job_id"], "running", "writing_artifacts", 85, "Writing analysis outputs", {
        "line_count": len(lines),
        "finding_count": len(findings),
    })

    result = {
        "schema_name": job_spec["output_schema"]["name"],
        "schema_version": "1.0.0",
        "payload": {
            "summary": summary,
            "top_findings": findings,
            "timeline": timeline,
            "suggested_next_steps": suggested_next_steps,
        },
    }

    write_json(output_dir / "result.json", result)
    write_json(output_dir / "artifacts.json", artifacts)
    emit_heartbeat(heartbeat_path, job_spec["job_id"], "completed", "completed", 100, "Log analysis ready", {
        "line_count": len(lines),
        "finding_count": len(findings),
    })
    return 0


def aggregate_child_results(job_spec, output_dir, heartbeat_path, task_params, child_job_ids):
    run_root = Path(task_params.get("_broker_run_root", ".broker/runs"))
    timeout_seconds = int(task_params.get("aggregate_wait_seconds", 60))
    allow_partial = bool(task_params.get("allow_partial_reduce", True))
    deadline = time.time() + timeout_seconds

    emit_heartbeat(heartbeat_path, job_spec["job_id"], "running", "waiting_for_children", 20, "Waiting for child log shard results", {
        "children_total": len(child_job_ids),
    })

    child_results = []
    failed_children = []
    while time.time() < deadline:
        pending, child_results, failed_children = collect_child_outcomes(run_root, child_job_ids)
        if not pending:
            break
        emit_heartbeat(heartbeat_path, job_spec["job_id"], "running", "waiting_for_children", 40, "Reducer is waiting for log shard completion", {
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

    emit_heartbeat(heartbeat_path, job_spec["job_id"], "running", "aggregating_results", 75, "Aggregating child log analyses", {
        "children_total": len(child_job_ids),
        "children_succeeded": len(child_results),
        "children_failed": len(failed_children),
    })

    payloads = [result.get("payload", {}) for result in child_results]
    findings = dedupe_findings(flatten_lists(payloads, "top_findings"), 8)
    timeline = flatten_lists(payloads, "timeline")[:12]
    next_steps = dedupe_strings(flatten_lists(payloads, "suggested_next_steps"), 8)
    warnings = []
    if failed_children or pending:
        warnings.append("partial_reduce_incomplete_children")
    result = {
        "schema_name": job_spec["output_schema"]["name"],
        "schema_version": "1.0.0",
        "payload": {
            "summary": build_aggregate_summary(payloads, findings),
            "top_findings": findings,
            "timeline": timeline,
            "suggested_next_steps": next_steps,
            "warnings": warnings,
            "aggregate_metrics": {
                "children_total": len(child_job_ids),
                "children_succeeded": len(child_results),
                "children_failed": len(failed_children) + len(pending),
                "coverage_fraction": len(child_results) / len(child_job_ids),
            },
        },
    }

    write_json(output_dir / "result.json", result)
    write_json(output_dir / "artifacts.json", [])
    emit_heartbeat(heartbeat_path, job_spec["job_id"], "completed", "completed", 100, "Aggregated log analysis ready", {
        "children_total": len(child_job_ids),
        "children_succeeded": len(child_results),
        "children_failed": len(failed_children) + len(pending),
        "finding_count": len(findings),
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


def derive_findings(lines):
    findings = []
    joined = "\n".join(lines)
    for code, needle, severity, next_steps in ERROR_PATTERNS:
        if needle in joined:
            findings.append({
                "code": code,
                "severity": severity,
                "confidence": 0.9 if severity == "high" else 0.75,
                "_next_steps": next_steps,
            })
    if not findings and lines:
        findings.append({
            "code": "GENERIC_LOG_FAILURE",
            "severity": "medium",
            "confidence": 0.5,
            "_next_steps": [
                "Inspect the first error-like lines in the log.",
                "Correlate the failure with the surrounding build or test step.",
            ],
        })
    return findings[:5]


def build_summary(lines, findings):
    if not lines:
        return "The log is empty."
    if findings:
        primary = findings[0]["code"]
        return (
            f"The log contains {len(lines)} lines. "
            f"Primary classified issue: {primary}."
        )
    return f"The log contains {len(lines)} lines but no findings were extracted."


def build_timeline(lines):
    timeline = []
    if lines:
        timeline.append({
            "phase": "log_start",
            "timestamp_hint": extract_timestamp(lines[0]),
        })
        timeline.append({
            "phase": "failure",
            "timestamp_hint": extract_timestamp(find_failure_line(lines)),
        })
    return timeline


def derive_next_steps(findings):
    steps = []
    for finding in findings:
        for step in finding.pop("_next_steps", []):
            if step not in steps:
                steps.append(step)
    return steps[:5]


def build_excerpt(lines):
    excerpt = []
    for line in lines:
        if looks_interesting(line):
            excerpt.append(redact(line.rstrip()))
        if len(excerpt) == 20:
            break
    if not excerpt:
        excerpt = [redact(line.rstrip()) for line in lines[:20]]
    return "\n".join(excerpt)


def looks_interesting(line):
    lowered = line.lower()
    return any(token in lowered for token in ["error", "failed", "exception", "fatal", "undefined"])


def redact(line):
    for token in ["Bearer ", "bearer ", "token=", "apikey=", "api_key="]:
        if token in line:
            prefix, _sep, _rest = line.partition(token)
            return prefix + token + "[REDACTED]"
    return line


def extract_timestamp(line):
    if not line:
        return ""
    return line[:32]


def find_failure_line(lines):
    for line in reversed(lines):
        if looks_interesting(line):
            return line
    return lines[-1] if lines else ""


def flatten_lists(payloads, key):
    items = []
    for payload in payloads:
        value = payload.get(key, [])
        if isinstance(value, list):
            items.extend(value)
    return items


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


def dedupe_findings(items, limit):
    seen = set()
    output = []
    for item in items:
        if not isinstance(item, dict):
            continue
        code = item.get("code")
        if code in seen:
            continue
        seen.add(code)
        output.append(item)
        if len(output) == limit:
            break
    return output


def build_aggregate_summary(payloads, findings):
    if findings:
        primary = findings[0].get("code", "UNKNOWN")
        return f"Aggregated {len(payloads)} log shard analyses. Primary classified issue: {primary}."
    return f"Aggregated {len(payloads)} log shard analyses with no merged findings."


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
