#!/usr/bin/env python3

import argparse
import json
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path
from socketserver import ThreadingMixIn


def parse_args():
    parser = argparse.ArgumentParser(description="Fake OpenAI-compatible server for RAG smoke tests")
    parser.add_argument("--listen-host", default="127.0.0.1")
    parser.add_argument("--listen-port", type=int, required=True)
    parser.add_argument("--count-file", required=True)
    return parser.parse_args()


class Server(ThreadingMixIn, HTTPServer):
    daemon_threads = True

    def __init__(self, addr, handler, count_file):
        super().__init__(addr, handler)
        self.count_file = Path(count_file)
        self.count_file.write_text("0", encoding="utf-8")

    def bump(self):
        count = int(self.count_file.read_text(encoding="utf-8").strip() or "0")
        self.count_file.write_text(str(count + 1), encoding="utf-8")


class Handler(BaseHTTPRequestHandler):
    server: Server

    def do_GET(self):
        if self.path == "/healthz":
            self._write_json(200, {"status": "ok"})
            return
        self.send_error(404)

    def do_POST(self):
        if self.path != "/v1/chat/completions":
            self.send_error(404)
            return
        content_length = int(self.headers.get("Content-Length", "0"))
        body = self.rfile.read(content_length) if content_length else b"{}"
        payload = json.loads(body.decode("utf-8"))
        self.server.bump()

        messages = payload.get("messages") or []
        system_text = ""
        if messages and isinstance(messages[0], dict):
            system_text = str(messages[0].get("content") or "")

        if "ordered_chunk_ids" in system_text:
            user_payload = {}
            if len(messages) > 1 and isinstance(messages[1], dict):
                try:
                    user_payload = json.loads(str(messages[1].get("content") or "{}"))
                except json.JSONDecodeError:
                    user_payload = {}
            candidates = user_payload.get("candidates") or []
            ordered_ids = [item.get("chunk_id", "") for item in candidates if isinstance(item, dict) and item.get("chunk_id")]
            content = json.dumps({"ordered_chunk_ids": ordered_ids[:5]}, ensure_ascii=True)
        else:
            user_payload = {}
            if len(messages) > 1 and isinstance(messages[1], dict):
                try:
                    user_payload = json.loads(str(messages[1].get("content") or "{}"))
                except json.JSONDecodeError:
                    user_payload = {}
            text = str(user_payload.get("text") or "")
            compressed = " ".join(text.split()[:24])
            content = json.dumps({"compressed_text": compressed}, ensure_ascii=True)

        self._write_json(200, {
            "id": "chatcmpl-fake",
            "object": "chat.completion",
            "choices": [{
                "index": 0,
                "message": {"role": "assistant", "content": content},
                "finish_reason": "stop",
            }],
        })

    def log_message(self, _fmt, *_args):
        return

    def _write_json(self, status, payload):
        body = json.dumps(payload, ensure_ascii=True).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


def main():
    args = parse_args()
    server = Server((args.listen_host, args.listen_port), Handler, args.count_file)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        return 0


if __name__ == "__main__":
    raise SystemExit(main())
