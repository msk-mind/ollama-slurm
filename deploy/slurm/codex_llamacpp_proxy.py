#!/usr/bin/env python3

import argparse
import json
import sys
import time
import urllib.error
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from typing import Any
from urllib.parse import urlsplit


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Compatibility proxy between Codex and llama.cpp's OpenAI-style server."
    )
    parser.add_argument("--listen-host", default="127.0.0.1")
    parser.add_argument("--listen-port", type=int, default=1234)
    parser.add_argument("--upstream", required=True, help="Base upstream URL, for example http://host:port")
    parser.add_argument(
        "--dump-dir",
        default="",
        help="Optional directory for request and response dumps used during protocol debugging.",
    )
    parser.add_argument(
        "--model-alias",
        action="append",
        default=[],
        help="Optional alias to expose in the rewritten model catalog, for example gpt-oss-20b.",
    )
    return parser.parse_args()


def now_ms() -> int:
    return int(time.time() * 1000)


def ensure_json_bytes(payload: Any) -> bytes:
    return json.dumps(payload, separators=(",", ":"), ensure_ascii=True).encode("utf-8")


def make_model_entry(slug: str, description: str) -> dict[str, Any]:
    return {
        "slug": slug,
        "id": slug,
        "display_name": slug,
        "description": description,
        "base_instructions": "",
        "priority": 0,
        "visibility": "list",
        "supported_in_api": True,
        "shell_type": "shell_command",
        "default_reasoning_level": "medium",
        "supported_reasoning_levels": [
            {"effort": "low", "description": "Fast responses with lighter reasoning"},
            {"effort": "medium", "description": "Balanced local reasoning"},
            {"effort": "high", "description": "Deeper local reasoning"},
        ],
        "additional_speed_tiers": [],
        "service_tiers": [],
        "availability_nux": None,
        "upgrade": None,
        "supports_reasoning_summaries": False,
        "default_reasoning_summary": "none",
        "support_verbosity": False,
        "default_verbosity": "low",
        "apply_patch_tool_type": "freeform",
        "web_search_tool_type": "text_and_image",
        "supports_parallel_tool_calls": True,
        "supports_image_detail_original": False,
        "input_modalities": ["text"],
        "supports_search_tool": False,
        "use_responses_lite": False,
        "experimental_supported_tools": [],
        "truncation_policy": {"mode": "tokens", "limit": 65536},
        "context_window": 65536,
        "max_context_window": 65536,
        "effective_context_window_percent": 100,
    }


def normalize_models_response(payload: Any, aliases: list[str]) -> Any:
    data = payload.get("data")
    if isinstance(data, list) and data:
        models = []
        for item in data:
            if not isinstance(item, dict):
                continue
            model_id = item.get("id", "")
            description = item.get("owned_by", "")
            if model_id:
                models.append(make_model_entry(model_id, description))
            stem = model_id[:-5] if model_id.endswith(".gguf") else model_id
            if stem and stem != model_id:
                models.append(make_model_entry(stem, description))
            for alias in aliases:
                models.append(make_model_entry(alias, description))
        deduped = []
        seen = set()
        for model in models:
            slug = model["slug"]
            if slug in seen:
                continue
            seen.add(slug)
            deduped.append(model)
        return {"models": deduped}
    return payload


def rewrite_tool(tool: Any) -> Any:
    if not isinstance(tool, dict):
        return tool
    tool_type = tool.get("type")
    if tool_type == "function":
        return tool

    rewritten = dict(tool)
    name = rewritten.get("name")
    if not name:
        name = tool_type or "tool"
    rewritten["type"] = "function"
    rewritten["name"] = str(name)
    rewritten.setdefault("description", f"Codex tool shim for {name}.")
    rewritten.setdefault("parameters", {"type": "object", "properties": {}, "additionalProperties": True})
    return rewritten


def rewrite_responses_request(payload: Any) -> Any:
    if not isinstance(payload, dict):
        return payload
    rewritten = dict(payload)
    tools = rewritten.get("tools")
    if isinstance(tools, list):
        rewritten["tools"] = [rewrite_tool(tool) for tool in tools]
    return rewritten


class ProxyServer(ThreadingHTTPServer):
    def __init__(
        self,
        server_address: tuple[str, int],
        handler_class: type[BaseHTTPRequestHandler],
        upstream: str,
        dump_dir: str,
        model_aliases: list[str],
    ):
        super().__init__(server_address, handler_class)
        self.upstream = upstream.rstrip("/")
        self.dump_dir = Path(dump_dir) if dump_dir else None
        self.model_aliases = model_aliases
        if self.dump_dir is not None:
            self.dump_dir.mkdir(parents=True, exist_ok=True)

    def dump(self, prefix: str, payload: bytes) -> None:
        if self.dump_dir is None:
            return
        filename = self.dump_dir / f"{now_ms()}_{prefix}.json"
        filename.write_bytes(payload)


class Handler(BaseHTTPRequestHandler):
    server: ProxyServer

    def do_GET(self) -> None:
        self._handle()

    def do_POST(self) -> None:
        self._handle()

    def log_message(self, fmt: str, *args: Any) -> None:
        sys.stderr.write("%s - - [%s] %s\n" % (self.address_string(), self.log_date_time_string(), fmt % args))

    def _handle(self) -> None:
        route_path = urlsplit(self.path).path
        content_length = int(self.headers.get("Content-Length", "0"))
        body = self.rfile.read(content_length) if content_length else b""

        upstream_body = body
        if route_path == "/v1/responses" and body:
            self.server.dump("responses_request_raw", body)
            try:
                payload = json.loads(body.decode("utf-8"))
            except json.JSONDecodeError:
                payload = None
            if payload is not None:
                rewritten = rewrite_responses_request(payload)
                upstream_body = ensure_json_bytes(rewritten)
                self.server.dump("responses_request_rewritten", upstream_body)

        url = self.server.upstream + self.path
        headers = {
            key: value
            for key, value in self.headers.items()
            if key.lower() not in {"host", "content-length", "connection"}
        }
        request = urllib.request.Request(url, data=upstream_body if self.command != "GET" else None, headers=headers, method=self.command)

        try:
            with urllib.request.urlopen(request) as response:
                response_body = response.read()
                status = response.status
                response_headers = response.headers
        except urllib.error.HTTPError as exc:
            response_body = exc.read()
            status = exc.code
            response_headers = exc.headers
        except Exception as exc:  # pragma: no cover - operational fallback
            self.send_error(502, explain=str(exc))
            return

        if route_path == "/v1/models":
            self.server.dump("models_response_raw", response_body)
            try:
                payload = json.loads(response_body.decode("utf-8"))
            except json.JSONDecodeError:
                payload = None
            if payload is not None:
                response_body = ensure_json_bytes(normalize_models_response(payload, self.server.model_aliases))
                self.server.dump("models_response_rewritten", response_body)

        if route_path == "/v1/responses":
            self.server.dump("responses_response_raw", response_body)

        self.send_response(status)
        for key, value in response_headers.items():
            lowered = key.lower()
            if lowered in {"content-length", "transfer-encoding", "connection"}:
                continue
            self.send_header(key, value)
        self.send_header("Content-Length", str(len(response_body)))
        self.end_headers()
        self.wfile.write(response_body)


def main() -> int:
    args = parse_args()
    server = ProxyServer(
        (args.listen_host, args.listen_port),
        Handler,
        args.upstream,
        args.dump_dir,
        args.model_alias,
    )
    print(f"codex-llamacpp-proxy listening on http://{args.listen_host}:{args.listen_port} -> {args.upstream}", flush=True)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        return 0


if __name__ == "__main__":
    raise SystemExit(main())
