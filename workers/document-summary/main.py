#!/usr/bin/env python3

import argparse
import json
from datetime import datetime, timezone
from pathlib import Path
from urllib.parse import urlparse, unquote


def main():
    parser = argparse.ArgumentParser(description="Document summary worker")
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
    emit_heartbeat(heartbeat_path, job_spec["job_id"], "running", "preprocessing", 20, "Loading source document", {})

    input_refs = input_manifest.get("input_refs", [])
    if not input_refs:
        raise ValueError("document_summary requires at least one input ref")

    source_path = resolve_file_uri(input_refs[0]["uri"])
    text = source_path.read_text(encoding="utf-8", errors="replace")
    emit_heartbeat(heartbeat_path, job_spec["job_id"], "running", "running_model", 55, "Generating compact summary", {})

    lines = text.splitlines()
    words = text.split()
    summary_text = build_summary(text, lines, words)

    result = {
        "schema_name": job_spec["output_schema"]["name"],
        "schema_version": "1.0.0",
        "payload": {
            "summary": summary_text,
            "sections": [],
            "key_points": derive_key_points(lines),
            "open_questions": [],
            "source_metadata": {
                "path": str(source_path),
                "line_count": len(lines),
                "word_count": len(words),
                "character_count": len(text),
            },
        },
    }

    artifact_path = output_dir / "source_excerpt.txt"
    artifact_path.write_text(build_excerpt(lines), encoding="utf-8")
    emit_heartbeat(heartbeat_path, job_spec["job_id"], "running", "writing_artifacts", 85, "Writing structured outputs", {
        "line_count": len(lines),
        "word_count": len(words),
    })

    artifacts = [
        {
            "artifact_id": "artifact_source_excerpt",
            "artifact_type": "excerpt",
            "path": str(artifact_path),
            "classification": input_refs[0].get("classification", "unknown"),
        }
    ]

    write_json(output_dir / "result.json", result)
    write_json(output_dir / "artifacts.json", artifacts)
    emit_heartbeat(heartbeat_path, job_spec["job_id"], "completed", "completed", 100, "Document summary ready", {
        "line_count": len(lines),
        "word_count": len(words),
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


def build_summary(text, lines, words):
    if not text.strip():
        return "The document is empty."

    first_nonempty = next((line.strip() for line in lines if line.strip()), "")
    sentence = first_sentence(text)
    if sentence:
        return (
            f"The document contains {len(words)} words across {len(lines)} lines. "
            f"Opening sentence: {sentence}"
        )
    if first_nonempty:
        return (
            f"The document contains {len(words)} words across {len(lines)} lines. "
            f"Opening line: {first_nonempty[:240]}"
        )
    return f"The document contains {len(words)} words across {len(lines)} lines."


def derive_key_points(lines):
    points = []
    for line in lines:
        stripped = line.strip()
        if not stripped:
            continue
        if stripped.startswith(("-", "*", "1.", "2.", "3.")):
            points.append(stripped[:240])
        if len(points) == 5:
            break
    if points:
        return points

    first_nonempty = [line.strip()[:240] for line in lines if line.strip()]
    return first_nonempty[:3]


def build_excerpt(lines):
    excerpt = []
    for line in lines:
        if line.strip():
            excerpt.append(line.rstrip())
        if len(excerpt) == 20:
            break
    return "\n".join(excerpt)


def first_sentence(text):
    normalized = " ".join(text.split())
    for separator in [". ", "! ", "? "]:
        if separator in normalized:
            return normalized.split(separator, 1)[0][:240] + separator.strip()
    return normalized[:240]


if __name__ == "__main__":
    raise SystemExit(main())
