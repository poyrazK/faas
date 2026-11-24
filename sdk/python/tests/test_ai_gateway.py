import json
import threading
from http.server import BaseHTTPRequestHandler, HTTPServer

import pytest
from faas_sdk.preview import (
    AI_CALL_HEADER,
    AI_PROMPT_VERSION_HEADER,
    AI_SESSION_HEADER,
    AI_TAGS_HEADER,
    AI_TRACE_HEADER,
    AI_USER_HEADER,
    AIGatewayError,
    ai_gateway_feedback,
    ai_gateway_headers,
    ai_gateway_url,
    ai_prompt,
    is_valid_ai_user_id,
    opaque_ai_user_id,
)


def test_ai_gateway_url_addresses_an_integration_or_a_route():
    env = {"GREGALE_OUTBOUND_URL": "http://127.0.0.1:2774/"}
    assert ai_gateway_url(integration="int-1", env=env) == "http://127.0.0.1:2774/i/int-1"
    assert ai_gateway_url(route="route-1", env=env) == "http://127.0.0.1:2774/r/route-1"
    for kwargs in (
        {"route": "route-1", "env": {}},
        {"env": env},
        {"integration": "a", "route": "b", "env": env},
        {"route": "a/b", "env": env},
    ):
        with pytest.raises(AIGatewayError):
            ai_gateway_url(**kwargs)


def test_ai_gateway_headers_validate_ids_like_the_gateway():
    headers = ai_gateway_headers(user="user:42", session="chat-7")
    assert headers == {AI_USER_HEADER: "user:42", AI_SESSION_HEADER: "chat-7"}
    assert ai_gateway_headers() == {}
    for bad in ("", "has space", "x" * 65, "~other"):
        assert not is_valid_ai_user_id(bad)
        with pytest.raises(AIGatewayError):
            ai_gateway_headers(user=bad)


def test_opaque_ai_user_id_is_stable_valid_and_keyed():
    plain = opaque_ai_user_id("alice@example.com")
    keyed = opaque_ai_user_id("alice@example.com", "secret")
    assert plain == opaque_ai_user_id("alice@example.com")
    assert keyed == opaque_ai_user_id("alice@example.com", b"secret")
    assert keyed != plain
    assert is_valid_ai_user_id(plain) and is_valid_ai_user_id(keyed)


def test_ai_gateway_headers_name_tags_and_a_trace():
    headers = ai_gateway_headers(tags={"feature": "search", "tier": "free"}, trace="task-7")
    assert headers[AI_TAGS_HEADER] == "feature=search,tier=free"
    assert headers[AI_TRACE_HEADER] == "task-7"
    for kwargs in (
        {"tags": {"Feature": "search"}},
        {"tags": {key: "1" for key in "abcdef"}},
        {"tags": {}},
        {"trace": "has space"},
    ):
        with pytest.raises(AIGatewayError):
            ai_gateway_headers(**kwargs)


def test_ai_gateway_feedback_rates_a_call_through_the_relay():
    seen = []

    class Relay(BaseHTTPRequestHandler):
        def do_POST(self):  # noqa: N802 - http.server's method name
            seen.append((self.path, json.loads(self.rfile.read(int(self.headers["Content-Length"])))))
            self.send_response(204 if self.path.startswith("/i/int-1/") else 404)
            self.end_headers()

        def log_message(self, *args):
            pass

    server = HTTPServer(("127.0.0.1", 0), Relay)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    try:
        env = {"GREGALE_OUTBOUND_URL": f"http://127.0.0.1:{server.server_port}"}
        ai_gateway_feedback(integration="int-1", call_id="call-1", rating="bad", label="wrong_answer", env=env)
        assert seen[0] == (
            "/i/int-1/_gregale/feedback",
            {"call_id": "call-1", "rating": "bad", "label": "wrong_answer"},
        )
        with pytest.raises(AIGatewayError):
            ai_gateway_feedback(integration="int-2", call_id="call-1", rating="good", env=env)
        with pytest.raises(AIGatewayError):
            ai_gateway_feedback(integration="int-1", call_id="call-1", rating="meh", env=env)
    finally:
        server.shutdown()


def test_ai_prompt_calls_a_managed_prompt_by_name():
    seen = []

    class Relay(BaseHTTPRequestHandler):
        def do_POST(self):  # noqa: N802 - http.server's method name
            body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
            seen.append((self.path, body, self.headers.get(AI_USER_HEADER)))
            if "variables" not in body:
                payload = json.dumps({"code": "outbound_ai_prompt_variable_missing"}).encode()
                self.send_response(400)
            else:
                payload = json.dumps({"choices": [{"message": {"role": "assistant", "content": "Hello!"}}]}).encode()
                self.send_response(200)
                self.send_header(AI_PROMPT_VERSION_HEADER, "3")
                self.send_header(AI_CALL_HEADER, "call-9")
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(payload)))
            self.end_headers()
            self.wfile.write(payload)

        def log_message(self, *args):
            pass

    server = HTTPServer(("127.0.0.1", 0), Relay)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    try:
        env = {"GREGALE_OUTBOUND_URL": f"http://127.0.0.1:{server.server_port}/"}
        result = ai_prompt("support-reply").run(
            {"question": "hi"}, messages=[{"role": "user", "content": "more"}], version=3, user="user:42", env=env
        )
        assert (result.text, result.version, result.call_id) == ("Hello!", 3, "call-9")
        assert seen[0] == (
            "/p/support-reply",
            {"variables": {"question": "hi"}, "messages": [{"role": "user", "content": "more"}], "version": 3},
            "user:42",
        )
        with pytest.raises(AIGatewayError, match="outbound_ai_prompt_variable_missing"):
            ai_prompt("support-reply").run(env=env)
        with pytest.raises(AIGatewayError):
            ai_prompt("support-reply").run({"question": "hi"}, version=0, env=env)
        with pytest.raises(AIGatewayError):
            ai_prompt("Support_Reply")
    finally:
        server.shutdown()
