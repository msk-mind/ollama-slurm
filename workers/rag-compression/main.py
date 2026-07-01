#!/usr/bin/env python3

import argparse
import hashlib
import json
import os
import re
import shutil
import subprocess
import urllib.error
import urllib.request
from collections import Counter
from datetime import datetime, timezone
from pathlib import Path
from urllib.parse import unquote, urlparse


IGNORE_DIRS = {".git", ".broker", "__pycache__", ".pytest_cache", "node_modules"}
TEXT_EXTENSIONS = {
    ".c", ".cc", ".cpp", ".cs", ".go", ".h", ".hpp", ".java", ".js", ".json", ".md",
    ".py", ".rb", ".rs", ".sh", ".sql", ".toml", ".ts", ".tsx", ".txt", ".xml", ".yaml", ".yml",
}
ERROR_PATTERNS = [
    ("fatal error", "build_error"),
    ("undefined reference", "linker_error"),
    ("exception", "runtime_exception"),
    ("traceback", "runtime_exception"),
    ("failed", "test_failure"),
    ("error", "generic_error"),
]
CODE_PATTERNS = [
    ("func ", "function"),
    ("def ", "function"),
    ("class ", "class"),
    ("type ", "type"),
    ("interface ", "interface"),
]
TREE_SITTER_GRAMMAR_REPOS = {
    ".c": "tree-sitter-c",
    ".cc": "tree-sitter-cpp",
    ".cpp": "tree-sitter-cpp",
    ".cs": "tree-sitter-c-sharp",
    ".go": "tree-sitter-go",
    ".h": "tree-sitter-c",
    ".hpp": "tree-sitter-cpp",
    ".java": "tree-sitter-java",
    ".js": "tree-sitter-javascript",
    ".json": "tree-sitter-json",
    ".md": "tree-sitter-markdown",
    ".py": "tree-sitter-python",
    ".rb": "tree-sitter-ruby",
    ".rs": "tree-sitter-rust",
    ".sh": "tree-sitter-bash",
    ".sql": "tree-sitter-sql",
    ".toml": "tree-sitter-toml",
    ".ts": "tree-sitter-typescript",
    ".tsx": "tree-sitter-typescript",
    ".yaml": "tree-sitter-yaml",
    ".yml": "tree-sitter-yaml",
}


def main():
    parser = argparse.ArgumentParser(description="RAG compression worker")
    parser.add_argument("--job-spec", required=True)
    parser.add_argument("--execution-plan")
    parser.add_argument("--input-manifest", required=True)
    parser.add_argument("--output-dir", required=True)
    parser.add_argument("--heartbeat-path")
    args = parser.parse_args()

    job_spec = load_json(Path(args.job_spec))
    input_manifest = load_json(Path(args.input_manifest))
    output_dir = Path(args.output_dir)
    output_dir.mkdir(parents=True, exist_ok=True)
    heartbeat_path = Path(args.heartbeat_path) if args.heartbeat_path else output_dir / "heartbeat.json"
    execution_plan_path = Path(args.execution_plan) if args.execution_plan else output_dir / "execution_plan.json"
    execution_plan = load_optional_json(execution_plan_path)
    runtime_context = build_runtime_context(execution_plan)
    runtime_adapter = build_runtime_adapter(runtime_context)

    emit_heartbeat(heartbeat_path, job_spec["job_id"], "running", "discovering_inputs", 10, "Discovering local inputs", {})
    task_type = job_spec["task_type"]
    task_params = job_spec.get("task_params") or {}
    constraints = job_spec.get("constraints") or {}
    input_refs = input_manifest.get("input_refs") or []
    if not input_refs:
        raise ValueError(f"{task_type} requires at least one input ref")

    discovered = discover_inputs(input_refs)
    emit_heartbeat(heartbeat_path, job_spec["job_id"], "running", "chunking", 20, "Chunking local inputs", {
        "input_count": len(discovered),
    })
    chunks = chunk_inputs(discovered)

    emit_heartbeat(heartbeat_path, job_spec["job_id"], "running", "indexing", 30, "Indexing local chunks", {
        "chunk_count": len(chunks),
    })
    chunk_manifest = build_chunk_manifest(chunks)
    retrieval_plan = plan_retrieval(task_type, task_params, discovered, chunk_manifest, constraints)

    emit_heartbeat(heartbeat_path, job_spec["job_id"], "running", "retrieving", 42, "Retrieving candidate chunks", {
        "chunk_count": len(chunks),
    })
    query = extract_query(task_type, task_params)
    candidates, retrieval, retrieval_trace = execute_retrieval_plan(task_type, query, task_params, constraints, discovered, chunks, chunk_manifest, retrieval_plan)

    emit_heartbeat(heartbeat_path, job_spec["job_id"], "running", "reranking", 54, "Reranking retrieved chunks", {
        "candidate_count": len(candidates),
    })
    reranked = rerank_candidates(task_type, query, task_params, candidates, runtime_adapter)

    emit_heartbeat(heartbeat_path, job_spec["job_id"], "running", "deduplicating", 64, "Deduplicating candidate chunks", {
        "reranked_count": len(reranked),
    })
    selected_chunks = select_chunks(reranked, constraints, retrieval)
    rerank_result = build_rerank_result(candidates, reranked, selected_chunks)

    emit_heartbeat(heartbeat_path, job_spec["job_id"], "running", "compressing_evidence", 78, "Building evidence pack", {
        "chunks_retrieved": retrieval["chunks_retrieved"],
    })
    result, artifacts = build_result(
        task_type,
        job_spec,
        task_params,
        constraints,
        query,
        discovered,
        selected_chunks,
        retrieval,
        retrieval_plan,
        retrieval_trace,
        chunk_manifest,
        rerank_result,
        runtime_context,
        runtime_adapter,
        output_dir,
    )

    emit_heartbeat(heartbeat_path, job_spec["job_id"], "running", "json_validation", 88, "Validating structured outputs", {
        "evidence_count": len(result["payload"].get("evidence", [])),
    })
    emit_heartbeat(heartbeat_path, job_spec["job_id"], "running", "writing_artifacts", 94, "Writing evidence outputs", {
        "evidence_count": len(result["payload"].get("evidence", [])),
        "artifact_count": len(artifacts),
    })
    write_json(output_dir / "result.json", result)
    write_json(output_dir / "artifacts.json", artifacts)
    emit_heartbeat(heartbeat_path, job_spec["job_id"], "completed", "completed", 100, "RAG worker completed", {
        "evidence_count": len(result["payload"].get("evidence", [])),
        "artifact_count": len(artifacts),
    })
    return 0


def load_json(path: Path):
    with path.open("r", encoding="utf-8") as handle:
        return json.load(handle)


def load_optional_json(path: Path):
    if not path.exists():
        return {}
    return load_json(path)


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


def build_runtime_context(execution_plan):
    execution_profile = execution_plan.get("execution_profile") or {}
    runtime_connection = execution_plan.get("runtime_connection") or {}
    return {
        "selected_model": str(execution_plan.get("selected_model") or execution_profile.get("model") or ""),
        "runtime_backend": str(execution_plan.get("runtime_backend") or execution_profile.get("runtime") or ""),
        "resource_tier": str(execution_plan.get("resource_tier") or execution_profile.get("tier") or ""),
        "accelerator": str(execution_profile.get("accelerator") or ""),
        "nodelist": str(execution_profile.get("nodelist") or ""),
        "constraint": str(execution_profile.get("constraint") or ""),
        "base_url": str(runtime_connection.get("base_url") or ""),
        "timeout_seconds": int(runtime_connection.get("timeout_seconds") or 20),
    }


def build_runtime_adapter(runtime_context):
    runtime_backend = str(runtime_context.get("runtime_backend") or "deterministic").strip().lower()
    selected_model = str(runtime_context.get("selected_model") or "").strip()
    base_url = str(runtime_context.get("base_url") or os.environ.get("RAG_LLAMA_CPP_BASE_URL") or os.environ.get("LLAMA_CPP_BASE_URL") or "").strip().rstrip("/")
    timeout_seconds = int(runtime_context.get("timeout_seconds") or os.environ.get("RAG_LLAMA_CPP_TIMEOUT_SECONDS") or "20")
    if runtime_backend == "llama.cpp":
        return {
            "name": "llama.cpp",
            "rerank_mode": "llama_cpp_api" if base_url else "heuristic_llama_cpp",
            "compression_mode": "llama_cpp_api" if base_url else "budgeted_llama_cpp",
            "backend_mode": "real" if base_url else "configured_local_llm",
            "backend_detail": selected_model or "llama.cpp",
            "base_url": base_url,
            "timeout_seconds": timeout_seconds,
            "llm_available": bool(base_url),
        }
    if runtime_backend == "vllm":
        return {
            "name": "vllm",
            "rerank_mode": "heuristic_vllm",
            "compression_mode": "budgeted_vllm",
            "backend_mode": "configured_local_llm",
            "backend_detail": selected_model or "vllm",
            "base_url": "",
            "timeout_seconds": timeout_seconds,
            "llm_available": False,
        }
    if runtime_backend == "sglang":
        return {
            "name": "sglang",
            "rerank_mode": "heuristic_sglang",
            "compression_mode": "budgeted_sglang",
            "backend_mode": "configured_local_llm",
            "backend_detail": selected_model or "sglang",
            "base_url": "",
            "timeout_seconds": timeout_seconds,
            "llm_available": False,
        }
    return {
        "name": "deterministic",
        "rerank_mode": "deterministic",
        "compression_mode": "deterministic",
        "backend_mode": "heuristic",
        "backend_detail": selected_model or "deterministic",
        "base_url": "",
        "timeout_seconds": timeout_seconds,
        "llm_available": False,
    }


def extract_query(task_type, task_params):
    if task_type == "rag_compress":
        return str(task_params.get("query") or "")
    if task_type == "debug_with_local_context":
        return str(task_params.get("problem") or "")
    if task_type == "summarize_logs":
        return str(task_params.get("query") or "Summarize the root failure and dominant warnings.")
    if task_type == "inspect_repo":
        return str(task_params.get("query") or "")
    if task_type == "propose_patch":
        return str(task_params.get("problem") or "")
    return ""


def discover_inputs(input_refs):
    discovered = []
    for idx, ref in enumerate(input_refs):
        uri = ref.get("uri", "")
        ref_type = ref.get("type", "")
        classification = ref.get("classification", "unknown")
        metadata = ref.get("metadata") or {}
        if uri.startswith("artifact://"):
            resolved_path = metadata.get("resolved_path", "")
            content = ""
            path = None
            if resolved_path:
                path = Path(resolved_path)
                if path.exists() and path.is_file():
                    content = path.read_text(encoding="utf-8", errors="replace")
            discovered.append({
                "id": f"input_{idx}",
                "type": ref_type or "artifact",
                "uri": uri,
                "classification": classification,
                "artifact_id": trim_artifact_prefix(uri),
                "artifact_type": metadata.get("artifact_type", ""),
                "source_job_id": metadata.get("source_job_id", ""),
                "path": path,
                "content": content,
            })
            continue
        path = resolve_file_uri(uri)
        if path.is_dir():
            discovered.append({
                "id": f"input_{idx}",
                "type": ref_type or "repo",
                "uri": uri,
                "classification": classification,
                "path": path,
                "content": "",
            })
            continue
        text = path.read_text(encoding="utf-8", errors="replace")
        discovered.append({
            "id": f"input_{idx}",
            "type": ref_type or "file",
            "uri": uri,
            "classification": classification,
            "path": path,
            "content": text,
        })
    return discovered


def resolve_file_uri(uri):
    parsed = urlparse(uri)
    if parsed.scheme != "file":
        raise ValueError(f"unsupported input uri: {uri}")
    return Path(unquote(parsed.path))


def chunk_inputs(discovered):
    chunks = []
    for item in discovered:
        if item["path"] is None:
            continue
        if item["path"].is_dir():
            chunks.extend(chunk_repo(item))
        else:
            chunks.extend(chunk_text_file(item))
    return chunks


def build_chunk_manifest(chunks):
    path_counts = Counter()
    input_type_counts = Counter()
    total_tokens = 0
    paths = []
    seen_paths = set()
    for chunk in chunks:
        path_counts[chunk["path"]] += 1
        input_type_counts[chunk["input_type"]] += 1
        total_tokens += chunk["token_estimate"]
        if chunk["path"] not in seen_paths and len(paths) < 64:
            seen_paths.add(chunk["path"])
            paths.append(chunk["path"])
    return {
        "chunk_count": len(chunks),
        "token_estimate": total_tokens,
        "path_count": len(path_counts),
        "paths": paths,
        "path_chunk_counts": dict(path_counts.most_common(24)),
        "input_type_counts": dict(input_type_counts),
    }


def plan_retrieval(task_type, task_params, discovered, chunk_manifest, constraints):
    requested = normalize_strategy_names(task_params.get("retrieval_strategies"))
    planned = requested or default_strategies(task_type)
    available = available_strategies(task_type, discovered)
    effective = [name for name in planned if name in available]
    skipped = [name for name in planned if name not in available]
    if not effective:
        effective = [name for name in default_strategies(task_type) if name in available]
    return {
        "task_type": task_type,
        "requested_strategies": requested,
        "effective_strategies": effective,
        "skipped_strategies": skipped,
        "available_strategies": available,
        "input_classifications": sorted({item.get("classification", "unknown") for item in discovered}),
        "input_types": sorted({item.get("type", "unknown") for item in discovered}),
        "path_count": chunk_manifest.get("path_count", 0),
        "chunk_count": chunk_manifest.get("chunk_count", 0),
        "retrieved_chunk_budget": int(constraints.get("retrieved_chunk_budget") or 64000),
    }


def normalize_strategy_names(value):
    if not value:
        return []
    if isinstance(value, list):
        items = value
    else:
        items = [value]
    names = []
    seen = set()
    for item in items:
        name = str(item).strip()
        if not name or name in seen:
            continue
        seen.add(name)
        names.append(name)
    return names


def available_strategies(task_type, discovered):
    strategies = []
    input_types = {item.get("type", "") for item in discovered}
    if "log" in input_types:
        strategies.extend(["ripgrep", "bm25"])
        if task_type == "debug_with_local_context":
            strategies.append("stack_trace_path")
    if "repo" in input_types or "file" in input_types:
        strategies.extend(["ripgrep", "bm25", "tree_sitter"])
        if task_type in {"debug_with_local_context", "propose_patch"}:
            strategies.append("git_diff_history")
    if "document" in input_types:
        strategies.append("embeddings")
    if "artifact" in input_types:
        strategies.append("artifact_context")
    ordered = []
    seen = set()
    for name in strategies:
        if name in seen:
            continue
        seen.add(name)
        ordered.append(name)
    return ordered


def chunk_repo(item):
    root = item["path"]
    chunks = []
    for path in sorted(root.rglob("*")):
        if path.is_dir() or should_skip(path, root):
            continue
        if path.suffix.lower() not in TEXT_EXTENSIONS and path.name != "go.mod":
            continue
        text = path.read_text(encoding="utf-8", errors="replace")
        rel = path.relative_to(root).as_posix()
        lines = text.splitlines()
        for idx in range(0, len(lines), 40):
            block = lines[idx:idx + 40]
            if not block:
                continue
            chunks.append(make_chunk(
                item=item,
                rel_path=rel,
                content="\n".join(block),
                line_start=idx + 1,
                line_end=idx + len(block),
            ))
    return chunks


def chunk_text_file(item):
    path = item["path"]
    lines = item["content"].splitlines()
    rel = path.name
    size = 80 if item["type"] == "log" else 40
    return [
        make_chunk(
            item=item,
            rel_path=rel,
            content="\n".join(lines[idx:idx + size]),
            line_start=idx + 1,
            line_end=min(idx + size, len(lines)),
        )
        for idx in range(0, len(lines), size)
        if lines[idx:idx + size]
    ]


def make_chunk(item, rel_path, content, line_start, line_end):
    chunk_hash = sha256_text(content)
    return {
        "chunk_id": f"chunk_{chunk_hash[:12]}_{line_start}",
        "chunk_hash": f"sha256:{chunk_hash}",
        "input_id": item["id"],
        "input_type": item["type"],
        "classification": item["classification"],
        "path": rel_path,
        "line_start": line_start,
        "line_end": line_end,
        "content": content,
        "token_estimate": estimate_tokens(content),
    }


def execute_retrieval_plan(task_type, query, task_params, constraints, discovered, chunks, chunk_manifest, retrieval_plan):
    query_terms = query_terms_for(query, task_type, task_params)
    executors = build_strategy_executors()
    executor_context = build_executor_context(discovered, query_terms)
    strategy_hits = Counter()
    strategy_stats = []
    merged = {}
    for strategy_name in retrieval_plan["effective_strategies"]:
        strategy_scored, execution_meta = strategy_candidates(executors, executor_context, strategy_name, task_type, query_terms, task_params, chunks)
        for candidate in strategy_scored:
            strategy_hits[strategy_name] += 1
            existing = merged.get(candidate["chunk_id"])
            if existing is None:
                merged[candidate["chunk_id"]] = candidate
                continue
            existing["score"] = round(existing["score"] + candidate["score"], 3)
            existing["reasons"] = unique_preserving_order(existing["reasons"] + candidate["reasons"])
            existing["matched_strategies"] = unique_preserving_order(existing["matched_strategies"] + candidate["matched_strategies"])
        strategy_stats.append({
            "strategy": strategy_name,
            "backend_mode": execution_meta["backend_mode"],
            "backend_detail": execution_meta["backend_detail"],
            "candidate_count": len(strategy_scored),
            "top_candidates": [
                {
                    "chunk_id": candidate["chunk_id"],
                    "path": candidate["path"],
                    "score": candidate["score"],
                }
                for candidate in strategy_scored[:5]
            ],
        })
    scored = sorted(merged.values(), key=lambda item: item["score"], reverse=True)
    if not scored:
        scored = [make_candidate(chunk, fallback_score(chunk, task_type), ["fallback"], ["fallback"]) for chunk in chunks[:24]]
        strategy_stats.append({
            "strategy": "fallback",
            "candidate_count": len(scored),
            "top_candidates": [
                {
                    "chunk_id": candidate["chunk_id"],
                    "path": candidate["path"],
                    "score": candidate["score"],
                }
                for candidate in scored[:5]
            ],
        })

    candidate_limit = min(max(int(constraints.get("retrieved_chunk_budget") or 64000) // 512, 8), 48)
    candidates = scored[:candidate_limit]
    budget = int(constraints.get("retrieved_chunk_budget") or 64000)

    retrieval = {
        "strategies": retrieval_plan["effective_strategies"],
        "requested_strategies": retrieval_plan["requested_strategies"],
        "skipped_strategies": retrieval_plan["skipped_strategies"],
        "chunks_considered": len(chunks),
        "chunks_indexed": chunk_manifest["chunk_count"],
        "chunks_retrieved": 0,
        "chunks_reranked": len(candidates),
        "chunks_deduplicated": 0,
        "chunks_compressed": 0,
        "retrieved_chunk_tokens": 0,
        "retrieved_chunk_budget": budget,
        "candidate_count": len(candidates),
        "strategy_hits": dict(strategy_hits),
        "strategy_stats": strategy_stats,
    }
    retrieval_trace = {
        "effective_strategies": retrieval_plan["effective_strategies"],
        "requested_strategies": retrieval_plan["requested_strategies"],
        "skipped_strategies": retrieval_plan["skipped_strategies"],
        "strategy_executions": strategy_stats,
        "merged_candidate_count": len(scored),
        "selected_candidate_count": len(candidates),
    }
    return candidates, retrieval, retrieval_trace


def build_executor_context(discovered, query_terms):
    discovered_by_id = {}
    search_roots = []
    seen_roots = set()
    for item in discovered:
        discovered_by_id[item["id"]] = item
        path = item.get("path")
        if path is None:
            continue
        root = path if path.is_dir() else path.parent
        root_key = str(root)
        if root_key in seen_roots:
            continue
        seen_roots.add(root_key)
        search_roots.append(root)
    return {
        "discovered_by_id": discovered_by_id,
        "query_terms": list(query_terms),
        "rg_path": shutil.which("rg"),
        "search_roots": search_roots,
        "rg_hits": {},
        "tree_sitter_path": shutil.which("tree-sitter") or "/gpfs/mskmind_ess/limr/repos/tree-sitter/target/release/tree-sitter",
        "tree_sitter_grammars": discover_tree_sitter_grammars(),
        "tree_sitter_tags": {},
    }


def discover_tree_sitter_grammars():
    candidates = []
    env_roots = os.environ.get("TREE_SITTER_GRAMMAR_ROOTS", "")
    for root in env_roots.split(":"):
        root = root.strip()
        if root:
            candidates.append(Path(root))

    cwd = Path.cwd()
    candidates.extend([
        cwd.parent,
        cwd.parent.parent,
        Path("/gpfs/mskmind_ess/limr/repos"),
    ])

    grammars = {}
    for repo_name in sorted(set(TREE_SITTER_GRAMMAR_REPOS.values())):
        for root in candidates:
            path = root / repo_name
            if path.is_dir():
                grammars[repo_name] = path
                break
    return grammars


def build_strategy_executors():
    return {
        "bm25": bm25_executor,
        "ripgrep": ripgrep_executor,
        "tree_sitter": tree_sitter_executor,
        "stack_trace_path": stack_trace_path_executor,
        "git_diff_history": git_diff_history_executor,
        "embeddings": embeddings_executor,
        "artifact_context": artifact_context_executor,
    }


def strategy_candidates(executors, executor_context, strategy_name, task_type, query_terms, task_params, chunks):
    executor = executors.get(strategy_name)
    if executor is None:
        return [], {"backend_mode": "unavailable", "backend_detail": "no_executor_registered"}
    scored = []
    backend_mode = "heuristic"
    backend_detail = "deterministic_executor"
    for chunk in chunks:
        score, reasons, meta = executor(executor_context, chunk, task_type, query_terms, task_params)
        if score <= 0:
            backend_mode = combine_backend_mode(backend_mode, meta.get("backend_mode", "heuristic"))
            if meta.get("backend_detail"):
                backend_detail = meta["backend_detail"]
            continue
        backend_mode = combine_backend_mode(backend_mode, meta.get("backend_mode", "heuristic"))
        if meta.get("backend_detail"):
            backend_detail = meta["backend_detail"]
        scored.append(make_candidate(chunk, score, reasons, [strategy_name]))
    scored.sort(key=lambda item: item["score"], reverse=True)
    return scored[:24], {"backend_mode": backend_mode, "backend_detail": backend_detail}


def combine_backend_mode(current, new_mode):
    priority = {"real": 3, "fallback": 2, "heuristic": 1, "unavailable": 0}
    if priority.get(new_mode, -1) > priority.get(current, -1):
        return new_mode
    return current


def make_candidate(chunk, score, reasons, matched_strategies):
    candidate = dict(chunk)
    candidate["score"] = round(score, 3)
    candidate["reasons"] = list(reasons)
    candidate["matched_strategies"] = list(matched_strategies)
    return candidate


def rerank_candidates(task_type, query, task_params, candidates, runtime_adapter):
    query_terms = query_terms_for(query, task_type, task_params)
    reranked = []
    for idx, candidate in enumerate(candidates):
        score = candidate["score"]
        path = candidate["path"].lower()
        if idx < 3:
            score += 0.4
        if any(term and term in path for term in query_terms):
            score += 0.8
        if task_type == "debug_with_local_context" and re.search(r"(trace|panic|error|fail)", candidate["content"].lower()):
            score += 0.5
        score += runtime_rerank_adjustment(runtime_adapter, candidate, task_type, query_terms)
        candidate = dict(candidate)
        candidate["rerank_score"] = round(score, 3)
        candidate["rerank_backend"] = runtime_adapter["name"]
        reranked.append(candidate)
    reranked = apply_runtime_rerank(task_type, query, task_params, reranked, runtime_adapter)
    reranked.sort(key=lambda item: item["rerank_score"], reverse=True)
    return reranked


def runtime_rerank_adjustment(runtime_adapter, candidate, task_type, query_terms):
    backend = runtime_adapter.get("name")
    text = candidate["content"].lower()
    if backend == "deterministic":
        return 0.0
    score = 0.15
    if task_type in {"inspect_repo", "propose_patch", "rag_compress"} and candidate["input_type"] in {"repo", "file"}:
        score += 0.2
    if task_type in {"debug_with_local_context", "summarize_logs"} and candidate["input_type"] == "log":
        score += 0.2
    if semantic_overlap_score(text, query_terms) > 0:
        score += 0.15
    return score


def apply_runtime_rerank(task_type, query, task_params, reranked, runtime_adapter):
    if runtime_adapter.get("name") != "llama.cpp" or not runtime_adapter.get("llm_available"):
        return reranked
    top = reranked[: min(len(reranked), 8)]
    if len(top) < 2:
        return reranked
    ordered_ids = llama_cpp_rerank_ids(task_type, query, task_params, top, runtime_adapter)
    if not ordered_ids:
        return reranked
    rank_map = {chunk_id: index for index, chunk_id in enumerate(ordered_ids)}
    adjusted = []
    for candidate in reranked:
        updated = dict(candidate)
        if candidate["chunk_id"] in rank_map:
            updated["rerank_score"] = round(updated["rerank_score"] + 1.0 - (rank_map[candidate["chunk_id"]] * 0.1), 3)
            updated["rerank_backend"] = "llama.cpp_api"
        adjusted.append(updated)
    return adjusted


def select_chunks(reranked, constraints, retrieval):
    selected = []
    used_budget = 0
    seen_hashes = set()
    duplicate_count = 0
    budget = int(constraints.get("retrieved_chunk_budget") or 64000)
    for candidate in reranked:
        if candidate["chunk_hash"] in seen_hashes:
            duplicate_count += 1
            continue
        projected = used_budget + candidate["token_estimate"]
        if selected and projected > budget:
            break
        selected.append(dict(candidate, relevance=round(min(candidate["rerank_score"] / 10.0, 0.99), 2)))
        used_budget = projected
        seen_hashes.add(candidate["chunk_hash"])
        if len(selected) >= 12:
            break

    retrieval["chunks_retrieved"] = len(selected)
    retrieval["chunks_deduplicated"] = duplicate_count
    retrieval["retrieved_chunk_tokens"] = used_budget
    return selected


def build_rerank_result(candidates, reranked, selected_chunks):
    selected_ids = {chunk["chunk_id"] for chunk in selected_chunks}
    return {
        "candidates": [
            {
                "chunk_id": candidate["chunk_id"],
                "path": candidate["path"],
                "line_start": candidate["line_start"],
                "line_end": candidate["line_end"],
                "score": candidate["score"],
                "reasons": candidate["reasons"],
                "matched_strategies": candidate["matched_strategies"],
            }
            for candidate in candidates[:16]
        ],
        "reranked": [
            {
                "chunk_id": candidate["chunk_id"],
                "path": candidate["path"],
                "line_start": candidate["line_start"],
                "line_end": candidate["line_end"],
                "rerank_score": candidate["rerank_score"],
                "rerank_backend": candidate.get("rerank_backend", ""),
                "selected": candidate["chunk_id"] in selected_ids,
            }
            for candidate in reranked[:16]
        ],
    }


def query_terms_for(query, task_type, task_params):
    parts = re.findall(r"[A-Za-z0-9_./:-]+", query.lower())
    if task_type == "debug_with_local_context":
        parts.extend(str(item).lower() for item in task_params.get("failing_tests") or [])
        parts.extend(str(item).lower() for item in task_params.get("suspect_paths") or [])
    if task_type == "inspect_repo":
        parts.extend(str(item).lower() for item in task_params.get("paths") or [])
    return [part for part in parts if len(part) > 1]


def bm25_executor(executor_context, chunk, task_type, query_terms, task_params):
    text = chunk["content"].lower()
    score = 0.0
    reasons = []
    for term in query_terms:
        if term in text:
            score += 2.0
            reasons.append("term_in_text")
    if task_type in {"summarize_logs", "debug_with_local_context"} and chunk["input_type"] == "log":
        score += log_signal_score(text)
        reasons.append("log_signal")
    return score, unique_preserving_order(reasons), {"backend_mode": "heuristic", "backend_detail": "deterministic_bm25"}


def ripgrep_executor(executor_context, chunk, task_type, query_terms, task_params):
    hits = ripgrep_hits_for_chunk(executor_context, chunk)
    if not hits:
        return ripgrep_fallback_executor(executor_context, chunk, task_type, query_terms, task_params)

    score = 0.0
    reasons = []
    for hit in hits:
        score += 2.5
        reasons.append("rg_match")
        if hit["line_number"] >= chunk["line_start"] and hit["line_number"] <= chunk["line_end"]:
            score += 1.0
            reasons.append("rg_line_in_chunk")
    if chunk["input_type"] == "log":
        score += min(len(hits), 3) * 0.5
        reasons.append("rg_log_density")
    return score, unique_preserving_order(reasons), {"backend_mode": "real", "backend_detail": "rg_cli"}


def tree_sitter_executor(executor_context, chunk, task_type, query_terms, task_params):
    if task_type not in {"inspect_repo", "rag_compress", "propose_patch"} or chunk["input_type"] not in {"repo", "file"}:
        return 0.0, [], {"backend_mode": "unavailable", "backend_detail": "input_type_not_supported"}
    tags = tree_sitter_tags_for_chunk(executor_context, chunk)
    if not tags:
        return tree_sitter_fallback_executor(executor_context, chunk, task_type, query_terms, task_params)

    score = 0.0
    reasons = []
    for tag in tags:
        score += 2.0
        reasons.append("tree_sitter_tag")
        name = tag.get("name", "").lower()
        for term in query_terms:
            if term in name:
                score += 1.0
                reasons.append("tree_sitter_name_match")
    return score, unique_preserving_order(reasons), {"backend_mode": "real", "backend_detail": "tree_sitter_cli_tags"}


def stack_trace_path_executor(executor_context, chunk, task_type, query_terms, task_params):
    if task_type != "debug_with_local_context":
        return 0.0, [], {"backend_mode": "unavailable", "backend_detail": "task_type_not_supported"}
    text = chunk["content"].lower()
    path = chunk["path"].lower()
    score = 0.0
    reasons = []
    if re.search(r"(trace|panic|error|fail)", text):
        score += 1.5
        reasons.append("stack_trace_signal")
    for suspect in task_params.get("suspect_paths") or []:
        if str(suspect).lower() in path:
            score += 2.0
            reasons.append("suspect_path_match")
    return score, unique_preserving_order(reasons), {"backend_mode": "heuristic", "backend_detail": "deterministic_stack_trace"}


def git_diff_history_executor(executor_context, chunk, task_type, query_terms, task_params):
    text = chunk["content"].lower()
    if task_type in {"debug_with_local_context", "propose_patch"} and ("fixme" in text or "todo" in text):
        return 0.75, ["change_risk_marker"], {"backend_mode": "heuristic", "backend_detail": "deterministic_git_history"}
    return 0.0, [], {"backend_mode": "heuristic", "backend_detail": "deterministic_git_history"}


def embeddings_executor(executor_context, chunk, task_type, query_terms, task_params):
    if chunk["input_type"] != "document":
        return 0.0, [], {"backend_mode": "unavailable", "backend_detail": "input_type_not_supported"}
    score = semantic_overlap_score(chunk["content"].lower(), query_terms)
    reasons = ["semantic_overlap"] if score > 0 else []
    return score, reasons, {"backend_mode": "heuristic", "backend_detail": "deterministic_embeddings"}


def artifact_context_executor(executor_context, chunk, task_type, query_terms, task_params):
    if chunk["input_type"] != "artifact":
        return 0.0, [], {"backend_mode": "unavailable", "backend_detail": "input_type_not_supported"}
    return 1.0, ["artifact_input"], {"backend_mode": "heuristic", "backend_detail": "artifact_metadata"}


def tree_sitter_fallback_executor(executor_context, chunk, task_type, query_terms, task_params):
    score = code_signal_score(chunk["content"].lower())
    reasons = ["code_structure_signal"] if score > 0 else []
    return score, reasons, {"backend_mode": "fallback", "backend_detail": "deterministic_code_structure"}


def ripgrep_fallback_executor(executor_context, chunk, task_type, query_terms, task_params):
    text = chunk["content"].lower()
    path = chunk["path"].lower()
    score = 0.0
    reasons = []
    for term in query_terms:
        if term in path:
            score += 3.0
            reasons.append("term_in_path")
        if term in text and chunk["input_type"] == "log":
            score += 1.5
            reasons.append("term_in_log_line")
    return score, unique_preserving_order(reasons), {"backend_mode": "fallback", "backend_detail": "deterministic_path_scan"}


def ripgrep_hits_for_chunk(executor_context, chunk):
    absolute_path = chunk_absolute_path(executor_context, chunk)
    if absolute_path is None:
        return []
    hits_by_path = ripgrep_search(executor_context)
    return hits_by_path.get(str(absolute_path), [])


def tree_sitter_tags_for_chunk(executor_context, chunk):
    absolute_path = chunk_absolute_path(executor_context, chunk)
    if absolute_path is None:
        return []
    all_tags = tree_sitter_tags(executor_context, absolute_path)
    if not all_tags:
        return []
    in_chunk = []
    for tag in all_tags:
        line_number = tag.get("line_number", 0)
        if line_number >= chunk["line_start"] and line_number <= chunk["line_end"]:
            in_chunk.append(tag)
    return in_chunk


def tree_sitter_tags(executor_context, absolute_path):
    cache = executor_context["tree_sitter_tags"]
    cache_key = str(absolute_path)
    if cache_key in cache:
        return cache[cache_key]

    tree_sitter_path = executor_context.get("tree_sitter_path")
    grammar_path = grammar_path_for_file(executor_context, absolute_path)
    if not tree_sitter_path or not Path(tree_sitter_path).exists() or grammar_path is None:
        cache[cache_key] = []
        return cache[cache_key]

    command = [
        tree_sitter_path,
        "tags",
        "-p",
        str(grammar_path),
        str(absolute_path),
    ]
    try:
        completed = subprocess.run(
            command,
            check=False,
            capture_output=True,
            text=True,
            encoding="utf-8",
            errors="replace",
        )
    except OSError:
        cache[cache_key] = []
        return cache[cache_key]
    if completed.returncode not in (0, 1):
        cache[cache_key] = []
        return cache[cache_key]

    tags = []
    for line in completed.stdout.splitlines():
        parsed = parse_tree_sitter_tag_line(line)
        if parsed is not None:
            tags.append(parsed)
    cache[cache_key] = tags
    return cache[cache_key]


def grammar_path_for_file(executor_context, absolute_path):
    repo_name = TREE_SITTER_GRAMMAR_REPOS.get(absolute_path.suffix.lower())
    if not repo_name:
        return None
    return executor_context.get("tree_sitter_grammars", {}).get(repo_name)


def parse_tree_sitter_tag_line(line):
    parts = line.split("\t")
    if len(parts) < 3:
        return None
    name = parts[0].strip()
    location = parts[2].strip()
    line_number = 0
    match = re.search(r"(\d+)", location)
    if match:
        line_number = int(match.group(1))
    return {
        "name": name,
        "line_number": line_number,
        "raw": line,
    }


def ripgrep_search(executor_context):
    cache = executor_context["rg_hits"]
    if "all" in cache:
        return cache["all"]

    rg_path = executor_context.get("rg_path")
    query_terms = executor_context.get("query_terms") or []
    if not rg_path or not query_terms:
        cache["all"] = {}
        return cache["all"]

    hits_by_path = {}
    for root in executor_context.get("search_roots") or []:
        for term in query_terms:
            command = [rg_path, "-n", "--no-heading", "--color", "never", "-F", term, str(root)]
            try:
                completed = subprocess.run(
                    command,
                    check=False,
                    capture_output=True,
                    text=True,
                    encoding="utf-8",
                    errors="replace",
                )
            except OSError:
                cache["all"] = {}
                return cache["all"]
            if completed.returncode not in (0, 1):
                continue
            for line in completed.stdout.splitlines():
                path_text, line_number, line_text = parse_ripgrep_line(line)
                if not path_text:
                    continue
                hits_by_path.setdefault(path_text, []).append({
                    "line_number": line_number,
                    "line_text": line_text,
                    "term": term,
                })

    cache["all"] = hits_by_path
    return hits_by_path


def parse_ripgrep_line(line):
    parts = line.split(":", 2)
    if len(parts) != 3:
        return "", 0, ""
    try:
        line_number = int(parts[1])
    except ValueError:
        line_number = 0
    return parts[0], line_number, parts[2]


def chunk_absolute_path(executor_context, chunk):
    discovered = executor_context.get("discovered_by_id") or {}
    item = discovered.get(chunk["input_id"])
    if item is None:
        return None
    path = item.get("path")
    if path is None:
        return None
    if path.is_dir():
        return path / chunk["path"]
    return path


def fallback_score(chunk, task_type):
    score = 1.0
    if task_type in {"summarize_logs", "debug_with_local_context"} and chunk["input_type"] == "log":
        score += log_signal_score(chunk["content"].lower())
    if task_type in {"inspect_repo", "rag_compress", "propose_patch"} and chunk["input_type"] in {"repo", "file"}:
        score += code_signal_score(chunk["content"].lower())
    return score


def log_signal_score(text):
    score = 0.0
    for needle, _kind in ERROR_PATTERNS:
        if needle in text:
            score += 2.5
    if re.search(r"\b(test|fail|panic|exception|traceback)\b", text):
        score += 2.0
    return score


def code_signal_score(text):
    score = 0.0
    for needle, _kind in CODE_PATTERNS:
        if needle in text:
            score += 1.5
    if "todo" in text or "fixme" in text:
        score += 0.5
    return score


def semantic_overlap_score(text, query_terms):
    if not query_terms:
        return 0.0
    score = 0.0
    for term in query_terms:
        if term in text:
            score += 0.6
    return score


def build_retrieval_policy_signals(retrieval_trace):
    executions = retrieval_trace.get("strategy_executions") or []
    mode_counts = Counter()
    degraded = []
    for execution in executions:
        mode = str(execution.get("backend_mode") or "unknown")
        mode_counts[mode] += 1
        if mode in {"fallback", "unavailable"}:
            degraded.append({
                "strategy": execution.get("strategy", ""),
                "backend_mode": mode,
                "backend_detail": execution.get("backend_detail", ""),
            })
    warnings = []
    if mode_counts.get("fallback", 0) > 0:
        warnings.append("LOCAL_RETRIEVAL_DEGRADED")
    if mode_counts.get("real", 0) == 0:
        warnings.append("NO_REAL_RETRIEVAL_BACKEND")
    return {
        "mode_counts": dict(mode_counts),
        "degraded_strategies": degraded,
        "real_backend_required_recommended": mode_counts.get("fallback", 0) > 0 or mode_counts.get("unavailable", 0) > 0,
        "warnings": warnings,
    }


def build_result(task_type, job_spec, task_params, constraints, query, discovered, selected_chunks, retrieval, retrieval_plan, retrieval_trace, chunk_manifest, rerank_result, runtime_context, runtime_adapter, output_dir):
    evidence = build_evidence(selected_chunks, constraints, runtime_adapter)
    evidence, budget_state = enforce_final_pack_budget(evidence, retrieval, constraints)
    retrieval["compression_backend"] = runtime_adapter["name"]
    retrieval["runtime_backend_mode"] = runtime_adapter["backend_mode"]
    retrieval["runtime_backend_detail"] = runtime_adapter["backend_detail"]
    policy_signals = build_retrieval_policy_signals(retrieval_trace)
    validation = build_validation_report(job_spec["output_schema"]["name"], evidence, retrieval, retrieval_plan, retrieval_trace, policy_signals, chunk_manifest, rerank_result, budget_state, runtime_context, runtime_adapter)
    warnings = list(validation["warnings"])
    artifacts = build_artifacts(task_type, output_dir, evidence, selected_chunks, retrieval, retrieval_plan, retrieval_trace, chunk_manifest, rerank_result, validation, runtime_context, runtime_adapter)

    if task_type == "rag_compress":
        payload = build_rag_payload(query, discovered, evidence, retrieval, retrieval_plan, retrieval_trace, policy_signals, constraints, warnings, runtime_context)
    elif task_type == "debug_with_local_context":
        payload = build_debug_payload(task_params, evidence, retrieval, constraints, warnings, runtime_context)
    elif task_type == "summarize_logs":
        payload = build_log_payload(evidence, warnings, runtime_context)
    elif task_type == "inspect_repo":
        payload = build_repo_inspection_payload(query, evidence, selected_chunks, warnings, runtime_context)
    elif task_type == "propose_patch":
        payload = build_patch_payload(task_params, evidence, warnings, runtime_context)
    else:
        raise ValueError(f"unsupported RAG task type: {task_type}")

    result = {
        "schema_name": job_spec["output_schema"]["name"],
        "schema_version": "1.0.0",
        "payload": payload,
    }
    return result, artifacts


def build_evidence(selected_chunks, constraints, runtime_adapter):
    budget_per_item = int(constraints.get("per_chunk_compression_budget") or 384)
    evidence = []
    for idx, chunk in enumerate(selected_chunks, start=1):
        excerpt = compress_text(chunk["content"], budget_per_item, runtime_adapter)
        evidence.append({
            "id": f"ev_{idx:03d}",
            "kind": classify_chunk_kind(chunk),
            "claim": summarize_chunk(chunk),
            "source_refs": [{
                "chunk_id": chunk["chunk_id"],
                "path": chunk["path"],
                "line_start": chunk["line_start"],
                "line_end": chunk["line_end"],
                "content_hash": chunk["chunk_hash"],
            }],
            "relevance": chunk.get("relevance", 0.5),
            "confidence": 0.82 if chunk.get("relevance", 0) >= 0.5 else 0.64,
            "redaction": "no_raw_excerpt",
            "excerpt_preview": excerpt,
            "compression_backend": runtime_adapter["name"],
        })
    return evidence


def build_rag_payload(query, discovered, evidence, retrieval, retrieval_plan, retrieval_trace, policy_signals, constraints, warnings, runtime_context):
    return {
        "query": query,
        "input_scope": {
            "classification": highest_classification(discovered),
            "source_refs": [source_ref(item) for item in discovered],
        },
        "retrieval": {
            "strategies": retrieval["strategies"],
            "chunks_considered": retrieval["chunks_considered"],
            "chunks_indexed": retrieval["chunks_indexed"],
            "chunks_retrieved": retrieval["chunks_retrieved"],
            "chunks_reranked": retrieval["chunks_reranked"],
            "chunks_deduplicated": retrieval["chunks_deduplicated"],
            "chunks_compressed": retrieval["chunks_compressed"],
            "requested_strategies": retrieval["requested_strategies"],
            "skipped_strategies": retrieval["skipped_strategies"],
            "strategy_hits": retrieval["strategy_hits"],
            "strategy_stats": retrieval["strategy_stats"],
            "compression_backend": retrieval.get("compression_backend", ""),
            "runtime_backend_mode": retrieval.get("runtime_backend_mode", ""),
            "runtime_backend_detail": retrieval.get("runtime_backend_detail", ""),
        },
        "retrieval_plan": retrieval_plan,
        "retrieval_trace": retrieval_trace,
        "policy_signals": policy_signals,
        "evidence": evidence,
        "budget": build_budget(retrieval["retrieved_chunk_tokens"], evidence, constraints),
        "provenance": build_provenance(runtime_context),
        "warnings": warnings,
    }


def build_debug_payload(task_params, evidence, retrieval, constraints, warnings, runtime_context):
    failing_tests = task_params.get("failing_tests") or []
    top_hypotheses = []
    for ev in evidence[:3]:
        top_hypotheses.append({
            "code": hypothesis_code(ev),
            "claim": ev["claim"],
            "confidence": ev["confidence"],
            "evidence_refs": [ev["id"]],
        })
    return {
        "problem": str(task_params.get("problem") or ""),
        "failure_signature": {
            "tests": failing_tests,
            "error_codes": [item["kind"] for item in evidence[:3]],
            "stack_trace_refs": [ev["id"] for ev in evidence if ev["kind"] == "runtime_exception"][:3],
        },
        "top_hypotheses": top_hypotheses,
        "evidence": evidence,
        "suggested_local_followups": [{
            "tool": "rag_compress",
            "query": "Narrow the investigation to the highest-confidence files and failing tests.",
        }] if evidence else [],
        "budget": build_budget(retrieval["retrieved_chunk_tokens"], evidence, constraints),
        "provenance": build_provenance(runtime_context),
        "warnings": warnings,
    }


def build_log_payload(evidence, warnings, runtime_context):
    timeline = []
    clusters = []
    cluster_counts = Counter(ev["kind"] for ev in evidence)
    for idx, ev in enumerate(evidence[:8], start=1):
        timeline.append({
            "phase": ev["kind"],
            "timestamp_hint": "",
            "evidence_refs": [ev["id"]],
        })
        if idx <= 4:
            clusters.append({
                "kind": ev["kind"],
                "count": cluster_counts[ev["kind"]],
                "representative_evidence_ref": ev["id"],
            })
    summary = "No log evidence was found."
    if evidence:
        summary = f"The first high-signal issue is {evidence[0]['kind']}; additional evidence was deduplicated into {len(clusters)} clusters."
    return {
        "summary": summary,
        "timeline": timeline,
        "clusters": clusters,
        "evidence": evidence,
        "provenance": build_provenance(runtime_context),
        "warnings": warnings,
    }


def build_repo_inspection_payload(query, evidence, selected_chunks, warnings, runtime_context):
    subsystems = []
    symbols = []
    seen_paths = set()
    for ev, chunk in zip(evidence[:6], selected_chunks[:6]):
        path = chunk["path"]
        top_dir = path.split("/", 1)[0]
        if top_dir not in seen_paths:
            subsystems.append({
                "name": top_dir,
                "paths": [top_dir],
                "evidence_refs": [ev["id"]],
            })
            seen_paths.add(top_dir)
        symbol = detect_symbol(chunk["content"])
        if symbol:
            symbols.append({
                "name": symbol,
                "path": path,
                "line_start": chunk["line_start"],
                "line_end": chunk["line_end"],
                "evidence_refs": [ev["id"]],
            })
    return {
        "query": query,
        "subsystems": subsystems,
        "symbols": symbols,
        "evidence": evidence,
        "provenance": build_provenance(runtime_context),
        "warnings": warnings,
    }


def build_patch_payload(task_params, evidence, warnings, runtime_context):
    allowed_paths = task_params.get("allowed_paths") or []
    target_path = allowed_paths[0] if allowed_paths else infer_patch_path(evidence)
    patch_ref = "artifact_patch_plan"
    return {
        "summary": "A candidate patch should tighten the failing path with the smallest change that matches the cited local evidence.",
        "patches": [{
            "patch_ref": patch_ref,
            "paths": [target_path] if target_path else [],
            "rationale": evidence[0]["claim"] if evidence else "No evidence was available; inspect inputs before changing code.",
            "evidence_refs": [ev["id"] for ev in evidence[:3]],
            "confidence": evidence[0]["confidence"] if evidence else 0.3,
            "policy": {
                "diff_inline": False,
                "release_requires_approval": True,
            },
        }],
        "validation_steps": task_params.get("validation_commands") or [],
        "provenance": build_provenance(runtime_context),
        "warnings": warnings,
    }


def build_artifacts(task_type, output_dir, evidence, selected_chunks, retrieval, retrieval_plan, retrieval_trace, chunk_manifest, rerank_result, validation, runtime_context, runtime_adapter):
    artifacts = []
    runtime_diagnostics = build_runtime_diagnostics(runtime_context, runtime_adapter)
    if task_type in {"rag_compress", "inspect_repo", "debug_with_local_context", "summarize_logs", "propose_patch"}:
        retrieval_plan_path = output_dir / "retrieval_plan.json"
        write_json(retrieval_plan_path, retrieval_plan)
        artifacts.append({
            "artifact_id": "artifact_retrieval_plan",
            "artifact_type": "retrieval_plan",
            "path": str(retrieval_plan_path),
            "classification": highest_classification(selected_chunks),
        })
        retrieval_trace_path = output_dir / "retrieval_trace.json"
        write_json(retrieval_trace_path, retrieval_trace)
        artifacts.append({
            "artifact_id": "artifact_retrieval_trace",
            "artifact_type": "retrieval_trace",
            "path": str(retrieval_trace_path),
            "classification": highest_classification(selected_chunks),
        })
        chunk_manifest_path = output_dir / "chunk_manifest.json"
        write_json(chunk_manifest_path, chunk_manifest)
        artifacts.append({
            "artifact_id": "artifact_chunk_manifest",
            "artifact_type": "chunk_manifest",
            "path": str(chunk_manifest_path),
            "classification": highest_classification(selected_chunks),
        })
        rerank_path = output_dir / "rerank_result.json"
        write_json(rerank_path, rerank_result)
        artifacts.append({
            "artifact_id": "artifact_rerank_result",
            "artifact_type": "rerank_result",
            "path": str(rerank_path),
            "classification": highest_classification(selected_chunks),
        })
        pack_path = output_dir / "evidence_pack.json"
        write_json(pack_path, {"evidence": evidence})
        artifacts.append({
            "artifact_id": "artifact_evidence_pack",
            "artifact_type": "evidence_pack",
            "path": str(pack_path),
            "classification": highest_classification(selected_chunks),
        })
        retrieval_path = output_dir / "retrieval_result.json"
        write_json(retrieval_path, {
            "strategies": retrieval["strategies"],
            "chunks_considered": retrieval["chunks_considered"],
            "chunks_indexed": retrieval["chunks_indexed"],
            "chunks_retrieved": retrieval["chunks_retrieved"],
            "chunks_reranked": retrieval["chunks_reranked"],
            "chunks_deduplicated": retrieval["chunks_deduplicated"],
            "requested_strategies": retrieval["requested_strategies"],
            "skipped_strategies": retrieval["skipped_strategies"],
            "strategy_hits": retrieval["strategy_hits"],
            "strategy_stats": retrieval["strategy_stats"],
            "compression_backend": runtime_adapter["name"],
            "runtime_backend_mode": runtime_adapter["backend_mode"],
            "runtime_backend_detail": runtime_adapter["backend_detail"],
            "runtime_diagnostics": runtime_diagnostics,
            "selected": [
                {
                    "chunk_id": chunk["chunk_id"],
                    "path": chunk["path"],
                    "line_start": chunk["line_start"],
                    "line_end": chunk["line_end"],
                    "relevance": chunk.get("relevance", 0.0),
                }
                for chunk in selected_chunks[:16]
            ],
        })
        artifacts.append({
            "artifact_id": "artifact_retrieval_result",
            "artifact_type": "retrieval_result",
            "path": str(retrieval_path),
            "classification": highest_classification(selected_chunks),
        })
    if task_type == "inspect_repo":
        index_path = output_dir / "retrieval_index.json"
        write_json(index_path, {
            "paths": [chunk["path"] for chunk in selected_chunks],
            "chunk_ids": [chunk["chunk_id"] for chunk in selected_chunks],
        })
        artifacts.append({
            "artifact_id": "artifact_repo_index",
            "artifact_type": "retrieval_result",
            "path": str(index_path),
            "classification": highest_classification(selected_chunks),
        })
    if task_type == "propose_patch":
        patch_path = output_dir / "patch_plan.json"
        write_json(patch_path, {"paths": infer_patch_paths(evidence), "evidence_refs": [ev["id"] for ev in evidence[:3]]})
        artifacts.append({
            "artifact_id": "artifact_patch_plan",
            "artifact_type": "patch_plan",
            "path": str(patch_path),
            "classification": highest_classification(selected_chunks),
        })
    validation_path = output_dir / "validation_report.json"
    write_json(validation_path, validation)
    artifacts.append({
        "artifact_id": "artifact_validation_report",
        "artifact_type": "validation_report",
        "path": str(validation_path),
        "classification": highest_classification(selected_chunks),
    })
    runtime_path = output_dir / "runtime_context.json"
    write_json(runtime_path, runtime_context)
    artifacts.append({
        "artifact_id": "artifact_runtime_context",
        "artifact_type": "structured_summary",
        "path": str(runtime_path),
        "classification": highest_classification(selected_chunks),
    })
    runtime_diagnostics_path = output_dir / "runtime_diagnostics.json"
    write_json(runtime_diagnostics_path, runtime_diagnostics)
    artifacts.append({
        "artifact_id": "artifact_runtime_diagnostics",
        "artifact_type": "runtime_diagnostics",
        "path": str(runtime_diagnostics_path),
        "classification": highest_classification(selected_chunks),
    })
    return artifacts


def build_budget(retrieved_chunk_tokens, evidence, constraints):
    compressed_tokens = sum(estimate_tokens(ev.get("claim", "")) for ev in evidence)
    final_pack_tokens = compressed_tokens + sum(estimate_tokens(ev.get("excerpt_preview", "")) for ev in evidence)
    return {
        "retrieved_chunk_tokens": retrieved_chunk_tokens,
        "compressed_tokens": compressed_tokens,
        "final_pack_tokens": final_pack_tokens,
        "remote_context_budget": int(constraints.get("remote_model_context_budget") or 0),
    }


def enforce_final_pack_budget(evidence, retrieval, constraints):
    budget_limit = int(constraints.get("final_evidence_pack_budget") or 0)
    state = {
        "applied_final_pack_budget": budget_limit,
        "trimmed_evidence_count": 0,
        "budget_ok": True,
        "warnings": [],
    }
    if budget_limit <= 0:
        return evidence, state

    kept = list(evidence)
    while len(kept) > 1 and estimate_evidence_pack_tokens(kept) > budget_limit:
        kept.pop()

    if len(kept) < len(evidence):
        state["trimmed_evidence_count"] = len(evidence) - len(kept)
        state["warnings"].append("FINAL_EVIDENCE_PACK_TRIMMED")

    state["budget_ok"] = estimate_evidence_pack_tokens(kept) <= budget_limit
    retrieval["chunks_compressed"] = len(kept)
    return kept, state


def build_validation_report(schema_name, evidence, retrieval, retrieval_plan, retrieval_trace, policy_signals, chunk_manifest, rerank_result, budget_state, runtime_context, runtime_adapter):
    warnings = list(budget_state.get("warnings") or [])
    warnings.extend(policy_signals.get("warnings") or [])
    runtime_diagnostics = build_runtime_diagnostics(runtime_context, runtime_adapter)
    report = {
        "schema_name": schema_name,
        "pipeline_stages": [
            "input_discovery",
            "chunking",
            "local_indexing",
            "retrieval",
            "reranking",
            "deduplication",
            "evidence_preserving_compression",
            "json_validation",
        ],
        "evidence_count": len(evidence),
        "all_evidence_refs_present": all(bool(ev.get("source_refs")) for ev in evidence),
        "budget_ok": budget_state.get("budget_ok", True),
        "trimmed_evidence_count": budget_state.get("trimmed_evidence_count", 0),
        "retrieval_strategies": retrieval["strategies"],
        "requested_retrieval_strategies": retrieval.get("requested_strategies", []),
        "skipped_retrieval_strategies": retrieval.get("skipped_strategies", []),
        "path_count": chunk_manifest.get("path_count", 0),
        "candidate_count": len(rerank_result.get("candidates") or []),
        "chunks_indexed": retrieval.get("chunks_indexed", 0),
        "chunks_reranked": retrieval.get("chunks_reranked", 0),
        "chunks_deduplicated": retrieval.get("chunks_deduplicated", 0),
        "strategy_hit_count": len(retrieval.get("strategy_hits") or {}),
        "strategy_execution_count": len(retrieval_trace.get("strategy_executions") or []),
        "runtime_context": runtime_context,
        "runtime_adapter": runtime_adapter,
        "runtime_diagnostics": runtime_diagnostics,
        "policy_signals": policy_signals,
        "warnings": warnings,
    }
    if retrieval_plan.get("requested_strategies") and not retrieval["strategies"]:
        report["warnings"].append("NO_REQUESTED_RETRIEVAL_STRATEGY_EXECUTED")
    if not report["all_evidence_refs_present"]:
        report["warnings"].append("MISSING_EVIDENCE_REFS")
    return report


def estimate_evidence_pack_tokens(evidence):
    total = 0
    for ev in evidence:
        total += estimate_tokens(ev.get("claim", ""))
        total += estimate_tokens(ev.get("excerpt_preview", ""))
    return total


def classify_chunk_kind(chunk):
    text = chunk["content"].lower()
    for needle, kind in ERROR_PATTERNS:
        if needle in text:
            return kind
    for needle, kind in CODE_PATTERNS:
        if needle in text:
            return kind
    if chunk["input_type"] == "log":
        return "log_context"
    return "code_context"


def summarize_chunk(chunk):
    path = chunk["path"]
    kind = classify_chunk_kind(chunk)
    lines = chunk["content"].splitlines()
    first = next((line.strip() for line in lines if line.strip()), "")
    if kind in {"build_error", "linker_error", "runtime_exception", "test_failure", "generic_error"}:
        return f"{path}:{chunk['line_start']}-{chunk['line_end']} contains {kind.replace('_', ' ')} context: {truncate(first, 140)}"
    return f"{path}:{chunk['line_start']}-{chunk['line_end']} is relevant {kind.replace('_', ' ')} context for the query."


def hypothesis_code(ev):
    kind = str(ev["kind"]).upper()
    return f"ROOT_{kind}"


def detect_symbol(content):
    for line in content.splitlines():
        stripped = line.strip()
        if stripped.startswith("func "):
            return stripped.split("(")[0].replace("func ", "").strip()
        if stripped.startswith("def "):
            return stripped.split("(")[0].replace("def ", "").strip()
        if stripped.startswith("class "):
            return stripped.split(":")[0].replace("class ", "").strip()
    return ""


def infer_patch_path(evidence):
    for ev in evidence:
        refs = ev.get("source_refs") or []
        if refs and refs[0].get("path"):
            return refs[0]["path"]
    return ""


def infer_patch_paths(evidence):
    paths = []
    seen = set()
    for ev in evidence:
        for ref in ev.get("source_refs") or []:
            path = ref.get("path")
            if path and path not in seen:
                seen.add(path)
                paths.append(path)
    return paths


def compress_text(text, budget, runtime_adapter):
    if runtime_adapter.get("name") == "llama.cpp" and runtime_adapter.get("llm_available"):
        compressed = llama_cpp_compress_text(text, budget, runtime_adapter)
        if compressed:
            return truncate(compressed, 220)
    words = text.split()
    max_words = compression_word_budget(budget, runtime_adapter)
    return truncate(" ".join(words[:max_words]), 220)


def compression_word_budget(budget, runtime_adapter):
    backend = runtime_adapter.get("name")
    if backend == "deterministic":
        return max(8, budget // 8)
    if backend == "llama.cpp":
        return max(10, budget // 7)
    if backend in {"vllm", "sglang"}:
        return max(12, budget // 6)
    return max(8, budget // 8)


def truncate(text, limit):
    text = text.strip()
    if len(text) <= limit:
        return text
    return text[: limit - 3].rstrip() + "..."


def build_provenance(runtime_context):
    provenance = {}
    if runtime_context.get("selected_model"):
        provenance["model"] = runtime_context["selected_model"]
    if runtime_context.get("runtime_backend"):
        provenance["runtime_backend"] = runtime_context["runtime_backend"]
    if runtime_context.get("resource_tier"):
        provenance["resource_tier"] = runtime_context["resource_tier"]
    if runtime_context.get("accelerator"):
        provenance["accelerator"] = runtime_context["accelerator"]
    return provenance


def build_runtime_diagnostics(runtime_context, runtime_adapter):
    diagnostics = {
        "runtime_backend": runtime_context.get("runtime_backend", ""),
        "selected_model": runtime_context.get("selected_model", ""),
        "resource_tier": runtime_context.get("resource_tier", ""),
        "backend_name": runtime_adapter.get("name", ""),
        "backend_mode": runtime_adapter.get("backend_mode", ""),
        "backend_detail": runtime_adapter.get("backend_detail", ""),
        "llm_available": bool(runtime_adapter.get("llm_available")),
        "endpoint_configured": bool(runtime_context.get("base_url")),
    }
    if runtime_context.get("base_url"):
        diagnostics["base_url"] = runtime_context["base_url"]
    if runtime_context.get("timeout_seconds"):
        diagnostics["timeout_seconds"] = runtime_context["timeout_seconds"]
    if runtime_adapter.get("last_error"):
        diagnostics["last_error"] = runtime_adapter["last_error"]
    return diagnostics


def llama_cpp_rerank_ids(task_type, query, task_params, candidates, runtime_adapter):
    payload = {
        "task_type": task_type,
        "query": query,
        "failing_tests": task_params.get("failing_tests") or [],
        "candidates": [
            {
                "chunk_id": candidate["chunk_id"],
                "path": candidate["path"],
                "line_start": candidate["line_start"],
                "line_end": candidate["line_end"],
                "reasons": candidate.get("reasons", []),
                "preview": truncate(candidate["content"], 240),
            }
            for candidate in candidates
        ],
    }
    prompt = (
        "Return strict JSON with shape {\"ordered_chunk_ids\":[...]} ranking the most relevant chunks first. "
        "Use only the provided evidence. Prefer error sites, directly implicated code paths, and changed files."
    )
    response = llama_cpp_json_completion(runtime_adapter, prompt, payload)
    if not isinstance(response, dict):
        return []
    value = response.get("ordered_chunk_ids")
    if not isinstance(value, list):
        return []
    ordered = []
    seen = set()
    valid_ids = {candidate["chunk_id"] for candidate in candidates}
    for item in value:
        chunk_id = str(item).strip()
        if not chunk_id or chunk_id not in valid_ids or chunk_id in seen:
            continue
        seen.add(chunk_id)
        ordered.append(chunk_id)
    return ordered


def llama_cpp_compress_text(text, budget, runtime_adapter):
    payload = {
        "budget_words": compression_word_budget(budget, runtime_adapter),
        "text": truncate(text, 2000),
    }
    prompt = (
        "Return strict JSON with shape {\"compressed_text\":\"...\"}. "
        "Compress the text to preserve concrete technical evidence, paths, symbols, and error strings."
    )
    response = llama_cpp_json_completion(runtime_adapter, prompt, payload)
    if not isinstance(response, dict):
        return ""
    value = response.get("compressed_text")
    if not isinstance(value, str):
        return ""
    return value.strip()


def llama_cpp_json_completion(runtime_adapter, instruction, payload):
    base_url = str(runtime_adapter.get("base_url") or "").strip().rstrip("/")
    if not base_url:
        return None
    model = str(runtime_adapter.get("backend_detail") or runtime_adapter.get("name") or "llama.cpp")
    body = {
        "model": model,
        "temperature": 0,
        "response_format": {"type": "json_object"},
        "messages": [
            {"role": "system", "content": instruction},
            {"role": "user", "content": json.dumps(payload, ensure_ascii=True)},
        ],
    }
    request = urllib.request.Request(
        base_url + "/v1/chat/completions",
        data=json.dumps(body, ensure_ascii=True).encode("utf-8"),
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    try:
        with urllib.request.urlopen(request, timeout=float(runtime_adapter.get("timeout_seconds") or 20)) as response:
            raw = response.read().decode("utf-8", errors="replace")
    except (urllib.error.URLError, urllib.error.HTTPError, TimeoutError, ValueError) as exc:
        runtime_adapter["backend_mode"] = "unavailable"
        runtime_adapter["llm_available"] = False
        runtime_adapter["last_error"] = str(exc)
        return None

    try:
        outer = json.loads(raw)
        choices = outer.get("choices") or []
        if not choices:
            runtime_adapter["backend_mode"] = "unavailable"
            runtime_adapter["llm_available"] = False
            runtime_adapter["last_error"] = "missing choices in llama.cpp response"
            return None
        message = choices[0].get("message") or {}
        content = message.get("content")
        if isinstance(content, list):
            content = "".join(part.get("text", "") for part in content if isinstance(part, dict))
        if not isinstance(content, str):
            runtime_adapter["backend_mode"] = "unavailable"
            runtime_adapter["llm_available"] = False
            runtime_adapter["last_error"] = "missing string content in llama.cpp response"
            return None
        parsed = extract_json_object(content)
        if parsed is None:
            runtime_adapter["backend_mode"] = "unavailable"
            runtime_adapter["llm_available"] = False
            runtime_adapter["last_error"] = "invalid JSON content in llama.cpp response"
            return None
        return parsed
    except (json.JSONDecodeError, AttributeError, TypeError) as exc:
        runtime_adapter["backend_mode"] = "unavailable"
        runtime_adapter["llm_available"] = False
        runtime_adapter["last_error"] = str(exc)
        return None


def extract_json_object(text):
    text = text.strip()
    if not text:
        return None
    try:
        return json.loads(text)
    except json.JSONDecodeError:
        pass
    start = text.find("{")
    end = text.rfind("}")
    if start == -1 or end == -1 or end <= start:
        return None
    try:
        return json.loads(text[start:end + 1])
    except json.JSONDecodeError:
        return None


def source_ref(item):
    if item.get("artifact_id"):
        return f"artifact:{item['artifact_id']}"
    if item["path"] is None:
        return item["uri"]
    if item["path"].is_dir():
        return f"repo:{item['path']}"
    return f"file:{item['path']}"


def highest_classification(items):
    order = {"public": 0, "internal": 1, "restricted": 2, "phi": 3, "secret_adjacent": 4, "unknown": -1}
    best = "unknown"
    for item in items:
        value = item.get("classification", "unknown")
        if order.get(value, -1) > order.get(best, -1):
            best = value
    return best


def estimate_tokens(text):
    return max(1, len(text) // 4)


def should_skip(path, root):
    rel_parts = path.relative_to(root).parts
    return any(part in IGNORE_DIRS for part in rel_parts)


def default_strategies(task_type):
    if task_type == "summarize_logs":
        return ["ripgrep", "bm25"]
    if task_type == "inspect_repo":
        return ["tree_sitter", "ripgrep", "bm25"]
    if task_type == "debug_with_local_context":
        return ["stack_trace_path", "ripgrep", "git_diff_history"]
    if task_type == "propose_patch":
        return ["tree_sitter", "git_diff_history"]
    return ["ripgrep", "bm25", "tree_sitter"]


def unique_preserving_order(items):
    out = []
    seen = set()
    for item in items:
        if item in seen:
            continue
        seen.add(item)
        out.append(item)
    return out


def sha256_text(text):
    return hashlib.sha256(text.encode("utf-8")).hexdigest()


def trim_artifact_prefix(uri):
    prefix = "artifact://"
    if uri.startswith(prefix):
        return uri[len(prefix):]
    return uri


if __name__ == "__main__":
    raise SystemExit(main())
