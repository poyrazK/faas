import json
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

from faas_sdk.api.apps import create_app_tcp_listener, update_app_tcp_listener
from faas_sdk.client import AuthenticatedClient
from faas_sdk.models import CreateTCPListenerRequest, TCPListenerTLSConfig, UpdateTCPListenerRequest


def test_generated_tls_intent_survives_real_http_transport():
    captured = []

    class Handler(BaseHTTPRequestHandler):
        def do_POST(self):
            self.respond(201)

        def do_PATCH(self):
            self.respond(200)

        def respond(self, status):
            body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
            captured.append((self.command, self.path, self.headers["Authorization"], body))
            response = {
                "id": "listener",
                "name": "echo",
                "guest_port": 9000,
                "public_port": 40142,
                "protocol": "tcp",
                "enabled": False,
                "created_at": "2026-10-01T00:00:00Z",
                "updated_at": "2026-10-01T00:00:00Z",
                "tls": body.get("tls", {"mode": "passthrough"}),
            }
            encoded = json.dumps(response).encode()
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(encoded)))
            self.end_headers()
            self.wfile.write(encoded)

        def log_message(self, *_args):
            pass

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    worker = threading.Thread(target=server.serve_forever, daemon=True)
    worker.start()
    client = AuthenticatedClient(base_url=f"http://127.0.0.1:{server.server_port}", token="test-key")
    try:
        policy = TCPListenerTLSConfig(mode="terminate", hostname="echo.example")
        created = create_app_tcp_listener.sync(
            "app", client=client, body=CreateTCPListenerRequest(name="echo", guest_port=9000, tls=policy)
        )
        assert created.tls.to_dict() == policy.to_dict()
        updated = update_app_tcp_listener.sync(
            "app", "echo", client=client, body=UpdateTCPListenerRequest(tls=TCPListenerTLSConfig(mode="passthrough"))
        )
        assert updated.tls.mode == "passthrough"
        assert captured == [
            (
                "POST",
                "/v1/apps/app/tcp-listeners",
                "Bearer test-key",
                {"name": "echo", "guest_port": 9000, "tls": policy.to_dict()},
            ),
            ("PATCH", "/v1/apps/app/tcp-listeners/echo", "Bearer test-key", {"tls": {"mode": "passthrough"}}),
        ]
    finally:
        client.get_httpx_client().close()
        server.shutdown()
        server.server_close()
        worker.join(timeout=2)
