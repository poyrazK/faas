#!/usr/bin/env python3
"""Hermetic API fixture for the public deploy Action release canary."""

from __future__ import annotations

import argparse
import json
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path


DEPLOYMENT_ID = "00000000000000000000000000000001"


class Handler(BaseHTTPRequestHandler):
    request_log: Path

    def log_message(self, _format: str, *_args: object) -> None:
        return

    def _write_json(self, status: int, payload: dict[str, object]) -> None:
        body = json.dumps(payload).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self) -> None:  # noqa: N802 - BaseHTTPRequestHandler contract
        if self.path == "/healthz":
            self._write_json(200, {"status": "ok"})
            return
        self._write_json(404, {"status": 404, "code": "not_found"})

    def do_POST(self) -> None:  # noqa: N802 - BaseHTTPRequestHandler contract
        content_length = int(self.headers.get("Content-Length", "0"))
        if content_length:
            self.rfile.read(content_length)
        with self.request_log.open("a", encoding="utf-8") as log:
            log.write(f"POST {self.path}\n")

        if self.path == "/v1/apps":
            self._write_json(200, {"id": "app-action-ref-canary", "slug": "action-ref-canary"})
            return
        if self.path == "/v1/apps/action-ref-canary/deployments/source-ref":
            self._write_json(
                202,
                {
                    "id": DEPLOYMENT_ID,
                    "app_id": "app-action-ref-canary",
                    "build_id": "build-action-ref-canary",
                    "kind": "github",
                    "status": "queued",
                },
            )
            return
        self._write_json(404, {"status": 404, "code": "not_found"})


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--port", type=int, required=True)
    parser.add_argument("--request-log", type=Path, required=True)
    args = parser.parse_args()
    args.request_log.write_text("", encoding="utf-8")
    Handler.request_log = args.request_log
    ThreadingHTTPServer(("127.0.0.1", args.port), Handler).serve_forever()


if __name__ == "__main__":
    main()
