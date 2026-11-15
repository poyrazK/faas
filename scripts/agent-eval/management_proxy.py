"""Private, run-local API observation and one committed-reply fault for evals.

Only a dedicated evaluation account may use this proxy. It records counters and
opaque request identities, never tokens, bodies, cron configuration or replies.
"""
import hashlib
import json
import socket
import threading
import urllib.error
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import urlsplit


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *_args, **_kwargs):
        return None


class ManagementProxy:
    def __init__(self, upstream, token, app, drop_cron_reply=False):
        self.upstream, self.token = upstream.rstrip("/"), token
        self.target = f"/v1/apps/{app}/cron-deletions"
        self.drop_cron_reply = drop_cron_reply
        self.lock = threading.Lock()
        self.mutations = self.injected = self.changed_retries = self.unexpected = 0
        self.attempts = {}
        owner = self

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *_args):
                pass

            def forward(self):
                if self.headers.get("Authorization") != "Bearer " + owner.token or not self.path.startswith("/v1/"):
                    self.send_error(403)
                    return
                length = int(self.headers.get("Content-Length", "0"))
                if length < 0 or length > 2 * 1024 * 1024:
                    self.send_error(413)
                    return
                body = self.rfile.read(length)
                path = urlsplit(self.path).path
                with owner.lock:
                    if self.command not in {"GET", "HEAD", "OPTIONS"} and not path.endswith("/source-review"):
                        owner.mutations += 1
                        if self.command != "POST" or path != owner.target:
                            owner.unexpected += 1
                    if self.command == "POST" and path == owner.target:
                        try:
                            attempt = json.loads(body)["request_id"]
                        except (ValueError, KeyError, TypeError):
                            self.send_error(400)
                            return
                        digest = hashlib.sha256(body).hexdigest()
                        if attempt in owner.attempts and owner.attempts[attempt] != digest:
                            owner.changed_retries += 1
                        owner.attempts[attempt] = digest
                headers = {key: value for key, value in self.headers.items()
                           if key.lower() not in {"host", "connection", "content-length", "transfer-encoding"}}
                request = urllib.request.Request(owner.upstream + self.path, data=body if length else None,
                                                 headers=headers, method=self.command)
                try:
                    # A service redirect must never forward the evaluation key
                    # to another origin, or move requests around the recorder.
                    response = urllib.request.build_opener(NoRedirect()).open(request, timeout=60)
                except urllib.error.HTTPError as error:
                    response = error
                except (OSError, urllib.error.URLError):
                    self.send_error(502)
                    return
                with response:
                    payload = response.read()
                    with owner.lock:
                        drop = owner.drop_cron_reply and not owner.injected and self.command == "POST" and path == owner.target and 200 <= response.status < 300
                        if drop:
                            owner.injected += 1
                    if drop:
                        # The native request completed; only its reply is lost.
                        self.close_connection = True
                        self.connection.shutdown(socket.SHUT_RDWR)
                        return
                    self.send_response(response.status)
                    for key, value in response.headers.items():
                        if key.lower() in {"content-type", "retry-after", "www-authenticate"}:
                            self.send_header(key, value)
                    self.send_header("Content-Length", str(len(payload)))
                    self.end_headers()
                    self.wfile.write(payload)

            do_GET = do_HEAD = do_OPTIONS = do_POST = do_PATCH = do_PUT = do_DELETE = forward

        self.server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self.server.daemon_threads = True
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)

    def start(self):
        self.thread.start()
        return f"http://127.0.0.1:{self.server.server_port}"

    def report(self):
        with self.lock:
            return {"mutation_requests": self.mutations, "lost_replies": self.injected,
                    "cron_attempts": len(self.attempts), "changed_input_retries": self.changed_retries,
                    "unexpected_mutations": self.unexpected}

    def close(self):
        self.server.shutdown()
        self.server.server_close()
        self.thread.join(timeout=5)
