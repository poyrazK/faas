import http.client
import json
import threading
from datetime import UTC, datetime, timedelta
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from types import SimpleNamespace

import pytest

from faas_sdk.preview.issues import IssueReporter


def test_protocol_error_retries_without_stopping_worker(monkeypatch):
    received = []
    attempts = 0

    class Response:
        status = 202

        def __enter__(self):
            return self

        def __exit__(self, *args):
            pass

    class Opener:
        def open(self, request, timeout):
            nonlocal attempts
            attempts += 1
            if attempts == 1:
                raise http.client.BadStatusLine("malformed upstream response")
            received.append(json.loads(request.data))
            return Response()

    reporter = IssueReporter("http://localhost", "exports", "g_issue_test")
    reporter._opener = Opener()
    try:
        assert reporter.capture_exception(ValueError("first"))
        assert reporter.capture_exception(ValueError("second"))
        assert reporter.flush(timeout=3)
        assert [event["message"] for event in received] == ["first", "second"]
        assert reporter.stats() == {"queued": 0, "accepted": 2, "dropped": 0}
    finally:
        reporter.close(timeout=0)


def test_http_status_context_is_validated_and_delivered(monkeypatch):
    received = []
    monkeypatch.setattr(IssueReporter, "_send", lambda self, event: (received.append(event), True)[1])
    reporter = IssueReporter("http://localhost", "exports", "g_issue_test", grouping_strategy="failure-site-v1")
    try:
        assert reporter.capture_exception(ValueError("first"), route="/checkout", http_status=502)
        assert reporter.capture_exception(ValueError("second"), http_status=200)
        assert reporter.flush()
        assert received[0]["http_status"] == 502
        assert "http_status" not in received[1]
    finally:
        reporter.close(timeout=0)


def test_optional_correlation_is_validated_before_delivery(monkeypatch):
    received = []
    monkeypatch.setattr(IssueReporter, "_send", lambda self, event: (received.append(event), True)[1])
    reporter = IssueReporter("http://localhost", "exports", "g_issue_test")
    try:
        assert reporter.capture_exception(ValueError("bad"), trace_id="bad", span_id="0" * 16, invocation_id="bad")
        assert reporter.capture_exception(ValueError("good"), trace_id="A" * 32, span_id="B" * 16,
                                          invocation_id="C4F3D348-8842-4EEC-B6E4-9666096E5D48")
        assert reporter.flush()
        assert all(key not in received[0] for key in ("trace_id", "span_id", "invocation_id"))
        assert received[1]["trace_id"] == "a" * 32
        assert received[1]["span_id"] == "b" * 16
        assert received[1]["invocation_id"] == "c4f3d348-8842-4eec-b6e4-9666096e5d48"
    finally:
        reporter.close(timeout=0)


def test_payload_limit_preserves_grouping_independent_of_context(monkeypatch):
    received = []
    monkeypatch.setattr(IssueReporter, "_send", lambda self, event: (received.append(event), True)[1])
    frames = [SimpleNamespace(filename="x" * 512, name=f"f{i}", lineno=1) for i in range(32)]
    monkeypatch.setattr("traceback.extract_tb", lambda tb: frames)
    monkeypatch.setattr("traceback.format_exception", lambda *args, **kwargs: ["stack"])
    error = ValueError("outer")
    error.__cause__ = TypeError("middle")
    error.__cause__.__cause__ = RuntimeError("root")
    crumbs = [{"occurred_at": datetime.now(UTC).isoformat(), "category": "custom", "level": "info", "message": "\x01" * 256} for _ in range(16)]
    reporter = IssueReporter("http://localhost", "exports", "g_issue_test", grouping_strategy="cause-chain-v1")
    try:
        assert reporter.capture_exception(error)
        assert reporter.capture_exception(error, breadcrumbs=crumbs, route="\x01" * 256)
        assert reporter.flush()
        assert received[0]["causes"] == received[1]["causes"]
        assert received[0].get("causes_incomplete") == received[1].get("causes_incomplete")
        assert received[0]["frames"] == received[1]["frames"]
        assert all(len(json.dumps(event, ensure_ascii=False).encode()) <= 65536 for event in received)
        assert received[1]["breadcrumbs_dropped"] > 0
    finally:
        reporter.close(timeout=0)


def test_oversized_legacy_frames_fit_the_request_limit(monkeypatch):
    received = []
    monkeypatch.setattr(IssueReporter, "_send", lambda self, event: (received.append(event), True)[1])
    frames = [SimpleNamespace(filename="\x01" * 512, name=f"f{i}", lineno=1) for i in range(32)]
    monkeypatch.setattr("traceback.extract_tb", lambda tb: frames)
    monkeypatch.setattr("traceback.format_exception", lambda *args, **kwargs: ["stack"])
    reporter = IssueReporter("http://localhost", "exports", "g_issue_test")
    try:
        assert reporter.capture_exception(ValueError("failure"))
        assert reporter.flush()
        assert len(json.dumps(received[0], ensure_ascii=False).encode()) <= 65536
        assert received[0]["frames"][-1]["function"] == "f31"
    finally:
        reporter.close(timeout=0)


def test_invalid_breadcrumb_timestamps_do_not_drop_exception():
    received = []

    class Handler(BaseHTTPRequestHandler):
        def do_POST(self):
            received.append(json.loads(self.rfile.read(int(self.headers["Content-Length"]))))
            self.send_response(202)
            self.end_headers()

        def log_message(self, *args):
            pass

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    reporter = IssueReporter(f"http://127.0.0.1:{server.server_port}", "exports", "g_issue_test")
    try:
        stamps = ["yesterday", "2026-02-30T10:00:00Z", (datetime.now(UTC) + timedelta(minutes=10)).isoformat(), datetime.now(UTC).isoformat()]
        breadcrumbs = [{"occurred_at": stamp, "category": "custom", "level": "info", "message": "entry"} for stamp in stamps]
        assert reporter.capture_exception(ValueError("context"), breadcrumbs=breadcrumbs)
        assert reporter.flush()
        assert len(received) == 1
        assert len(received[0]["breadcrumbs"]) == 1
        assert received[0]["breadcrumbs_dropped"] == 3
    finally:
        reporter.close(timeout=0)
        server.shutdown()
        server.server_close()


def test_invalid_breadcrumb_unicode_does_not_stop_delivery():
    received = []

    class Handler(BaseHTTPRequestHandler):
        def do_POST(self):
            received.append(json.loads(self.rfile.read(int(self.headers["Content-Length"]))))
            self.send_response(202)
            self.end_headers()

        def log_message(self, *args):
            pass

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    reporter = IssueReporter(f"http://127.0.0.1:{server.server_port}", "exports", "g_issue_test")
    try:
        reporter.capture_exception(ValueError("first"), breadcrumbs=[{"occurred_at": "\ud800", "message": "bad"}])
        reporter.capture_exception(ValueError("second"))
        assert reporter.flush()
        assert len(received) == 2
        assert received[0]["breadcrumbs_dropped"] == 1
        assert reporter._thread.is_alive()
        # If malformed text reaches serialization through a future field,
        # discard that event and continue delivering subsequent captures.
        with reporter._cv:
            reporter._queue.append({"message": "\ud800"})
            reporter._cv.notify_all()
        reporter.capture_exception(ValueError("third"))
        assert not reporter.flush()
        assert len(received) == 3
        assert reporter.stats()["dropped"] == 1
        assert reporter._thread.is_alive()
    finally:
        reporter.close(timeout=0)
        server.shutdown()
        server.server_close()


def test_flush_and_close_report_permanent_rejection():
    class Handler(BaseHTTPRequestHandler):
        def do_POST(self):
            self.send_response(401)
            self.end_headers()

        def log_message(self, *args):
            pass

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    reporter = IssueReporter(f"http://127.0.0.1:{server.server_port}", "exports", "g_issue_test")
    try:
        reporter.capture_exception(ValueError("rejected"))
        assert not reporter.flush()
        assert reporter.stats()["dropped"] == 1
        assert not reporter.close()
    finally:
        reporter.close(timeout=0)
        server.shutdown()
        server.server_close()


def test_rate_limit_retry_after():
    from faas_sdk.preview.issues import _retry_after

    assert _retry_after("60") == 60
    assert _retry_after(None) == 60
    assert _retry_after("invalid") == 60
    assert _retry_after("0") == 1


@pytest.mark.parametrize("status", [429, 503])
def test_retry_after_is_not_bypassed_by_flush(status):
    attempts = []
    first = threading.Event()

    class Handler(BaseHTTPRequestHandler):
        def do_POST(self):
            attempts.append(1)
            self.send_response(status)
            self.send_header("Retry-After", "60")
            self.end_headers()
            first.set()

        def log_message(self, *args):
            pass

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    reporter = IssueReporter(f"http://127.0.0.1:{server.server_port}", "exports", "g_issue_test")
    try:
        reporter.capture_exception(ValueError("limited"))
        assert first.wait(2)
        assert not reporter.flush(timeout=0.1)
        assert len(attempts) == 1
    finally:
        reporter.close(timeout=0)
        server.shutdown()
        server.server_close()


def test_regrouping_models_round_trip_actions_and_original_identity():
    from faas_sdk.preview.models.issue_action_request import IssueActionRequest
    from faas_sdk.preview.models.issue_activity import IssueActivity
    from faas_sdk.preview.models.issue_detail import IssueDetail

    original = "11111111-1111-4111-8111-111111111111"
    canonical = "22222222-2222-4222-8222-222222222222"
    for action in ({"action": "merge", "target_issue_id": canonical}, {"action": "split", "member_issue_id": original}):
        assert IssueActionRequest.from_dict(action).to_dict() == action
    activity = {
        "id": original,
        "action": "merged",
        "original_issue_id": original,
        "created_at": "2026-10-11T12:00:00Z",
        "details": {"canonical_issue_id": canonical},
    }
    assert str(IssueActivity.from_dict(activity).original_issue_id) == original
    identity = {
        "id": original,
        "app_id": canonical,
        "environment": "application",
        "fingerprint": "a" * 64,
        "grouping_version": 2,
        "title": "failure",
        "state": "open",
        "first_seen_at": "2026-10-11T12:00:00Z",
        "last_seen_at": "2026-10-11T12:00:00Z",
        "event_count": 2,
        "regression_count": 0,
    }
    occurrence = {
        "event_id": original,
        "occurred_at": "2026-10-11T12:00:00Z",
        "exception_type": "Error",
        "message": "failure",
        "original_issue_id": original,
        "deployment_id": canonical,
        "received_at": "2026-10-11T12:00:00Z",
        "attribution": "unattributed",
    }
    payload = {
        "issue": {**identity, "id": canonical},
        "group_members": [identity, {**identity, "id": canonical}],
        "events": [occurrence],
        "releases": [],
        "activity": [activity],
        "impact": {
            "window_start": "2026-10-11T12:00:00Z",
            "window_end": "2026-10-11T13:00:00Z",
            "identified_customers": 0,
            "observed_events": 1,
            "unattributed_events": 1,
            "coverage": "observed retained events",
        },
    }
    detail = IssueDetail.from_dict(payload)
    assert str(detail.events[0].original_issue_id) == original
    assert detail.group_members[0].event_count == 2
    assert detail.to_dict()["group_members"][0]["id"] == original


def test_issue_reporter_captures_context_without_locals():
    received = []

    class Handler(BaseHTTPRequestHandler):
        def do_POST(self):
            assert self.path == "/v1/apps/exports/issue-events"
            assert self.headers["Authorization"] == "Bearer g_issue_test"
            # Cloudflare rejects the default "Python-urllib/3.x" agent on api.gregale.dev.
            assert self.headers["User-Agent"].startswith("gregale-python-sdk/")
            received.append(json.loads(self.rfile.read(int(self.headers["Content-Length"]))))
            self.send_response(202)
            self.end_headers()

        def log_message(self, *args):
            pass

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    reporter = IssueReporter(f"http://127.0.0.1:{server.server_port}", "exports", "g_issue_test")
    try:
        try:
            raise ValueError("invalid format")
        except ValueError as error:
            event_id = reporter.capture_exception(error, route="/exports", trace_id="a" * 32, tenant_id="forged")
        assert reporter.flush()
        assert len(received) == 1
        assert received[0]["event_id"] == event_id
        assert received[0]["exception_type"] == "builtins.ValueError"
        assert received[0]["route"] == "/exports"
        assert received[0]["frames"]
        assert "tenant_id" not in received[0]
        assert "locals" not in received[0]
        assert "grouping_strategy" not in received[0]
        assert "frame_order" not in received[0]
    finally:
        reporter.close()
        server.shutdown()
        server.server_close()


def test_failure_site_reporter_preserves_grouping_controls_and_python_order():
    received = []

    class Handler(BaseHTTPRequestHandler):
        def do_POST(self):
            received.append(json.loads(self.rfile.read(int(self.headers["Content-Length"]))))
            self.send_response(503 if len(received) == 1 else 202)
            self.end_headers()

        def log_message(self, *args):
            pass

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    reporter = IssueReporter(
        f"http://127.0.0.1:{server.server_port}",
        "exports",
        "g_issue_test",
        grouping_strategy="failure-site-v1",
    )

    def fail():
        raise ValueError("invalid date")

    try:
        try:
            fail()
        except ValueError as error:
            reporter.capture_exception(
                error,
                error_code="INVALID_DATE",
                fingerprint_override="é" * 200,
                tenant_id="forged",
            )
        assert reporter.flush(timeout=5)
        assert len(received) == 2
        assert received[0] == received[1]
        event = received[0]
        assert event["grouping_strategy"] == "failure-site-v1"
        assert event["frame_order"] == "oldest-first"
        assert event["error_code"] == "INVALID_DATE"
        assert event["fingerprint_override"] == "é" * 128
        assert event["frames"][-1]["function"] == "fail"
        assert "tenant_id" not in event
    finally:
        reporter.close()
        server.shutdown()
        server.server_close()


def test_invalid_grouping_strategy_fails_before_starting_reporter():
    import pytest

    with pytest.raises(ValueError, match="grouping strategy"):
        IssueReporter("https://api.gregale.dev", "exports", "g_issue_test", grouping_strategy="unknown")


def test_issue_reporter_retries_exact_payload_and_preserves_exception():
    import asyncio

    received = []

    class Handler(BaseHTTPRequestHandler):
        def do_POST(self):
            received.append(json.loads(self.rfile.read(int(self.headers["Content-Length"]))))
            self.send_response(503 if len(received) == 1 else 202)
            self.end_headers()

        def log_message(self, *args):
            pass

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    reporter = IssueReporter(f"http://127.0.0.1:{server.server_port}", "exports", "g_issue_test")
    original = ValueError("failed")

    async def work():
        raise original

    try:
        try:
            asyncio.run(reporter.wrap(work)())
        except ValueError as error:
            assert error is original
        else:
            raise AssertionError("reporter swallowed application failure")
        assert reporter.flush()
        assert len(received) == 2
        assert received[0] == received[1]
        assert reporter.stats()["accepted"] == 1
    finally:
        reporter.close()
        server.shutdown()
        server.server_close()


def test_split_preview_client_preserves_unavailable_history_and_original_scope():
    from uuid import UUID

    import httpx

    from faas_sdk.preview.api.apps import preview_issue_split
    from faas_sdk.client import AuthenticatedClient
    from faas_sdk.preview.models.issue_split_matcher import IssueSplitMatcher
    from faas_sdk.preview.models.issue_split_preview import IssueSplitPreview

    original = UUID("11111111-1111-4111-8111-111111111111")
    canonical = "22222222-2222-4222-8222-222222222222"
    payload = {
        "original_issue_id": str(original),
        "canonical_issue_id": canonical,
        "matcher": {"route": "/checkout"},
        "lifetime_event_count": 20,
        "retained_event_count": 12,
        "matching_event_count": 9,
        "nonmatching_event_count": 3,
        "unavailable_history_event_count": 8,
        "matching_event_ids": [str(original)],
    }

    def handle(request):
        assert request.method == "POST"
        assert request.url.path == f"/v1/apps/payments/issues/{original}/split-preview"
        assert json.loads(request.content) == {"route": "/checkout"}
        return httpx.Response(200, json=payload)

    with httpx.Client(base_url="https://api.gregale.dev", transport=httpx.MockTransport(handle)) as transport:
        client = AuthenticatedClient(base_url="https://api.gregale.dev", token="read-token").set_httpx_client(transport)
        result = preview_issue_split.sync(
            "payments", original, client=client, body=IssueSplitMatcher(route="/checkout")
        )
    assert isinstance(result, IssueSplitPreview)
    assert result.to_dict() == payload


def test_split_rule_clients_preserve_routing_state_and_transfer_accounting():
    from uuid import UUID

    import httpx

    from faas_sdk.preview.api.apps import create_issue_split_rule, update_issue_split_rule
    from faas_sdk.client import AuthenticatedClient
    from faas_sdk.preview.models.issue_split_matcher import IssueSplitMatcher
    from faas_sdk.preview.models.issue_split_rule import IssueSplitRule
    from faas_sdk.preview.models.issue_split_rule_update import IssueSplitRuleUpdate

    source = UUID("11111111-1111-4111-8111-111111111111")
    rule_id = UUID("22222222-2222-4222-8222-222222222222")
    target = "33333333-3333-4333-8333-333333333333"
    payload = {
        "id": str(rule_id),
        "source_issue_id": str(source),
        "target_issue_id": target,
        "priority": 1,
        "matcher": {"route": "/checkout"},
        "enabled": True,
        "initial_moved_event_count": 3,
        "unavailable_history_event_count": 2,
        "created_at": "2026-10-11T18:00:00Z",
        "updated_at": "2026-10-11T18:00:00Z",
    }
    methods = []

    def handle(request):
        methods.append(request.method)
        if request.method == "POST":
            assert request.url.path == f"/v1/apps/payments/issues/{source}/split-rules"
            assert json.loads(request.content) == {"route": "/checkout"}
        else:
            assert request.method == "PATCH"
            assert request.url.path == f"/v1/apps/payments/issues/{source}/split-rules/{rule_id}"
            assert json.loads(request.content) == {"enabled": False}
            payload["enabled"] = False
        return httpx.Response(200, json=payload)

    with httpx.Client(base_url="https://api.gregale.dev", transport=httpx.MockTransport(handle)) as transport:
        client = AuthenticatedClient(base_url="https://api.gregale.dev", token="write-token").set_httpx_client(
            transport
        )
        created = create_issue_split_rule.sync(
            "payments", source, client=client, body=IssueSplitMatcher(route="/checkout")
        )
        assert isinstance(created, IssueSplitRule)
        assert (
            created.enabled and created.initial_moved_event_count == 3 and created.unavailable_history_event_count == 2
        )
        disabled = update_issue_split_rule.sync(
            "payments", source, rule_id, client=client, body=IssueSplitRuleUpdate(enabled=False)
        )
        assert isinstance(disabled, IssueSplitRule)
        assert not disabled.enabled and disabled.target_issue_id == created.target_issue_id
    assert methods == ["POST", "PATCH"]


def test_cause_capture_follows_python_semantics_and_bounds_cycles():
    received = []
    reporter = IssueReporter("https://api.gregale.dev", "exports", "g_issue_test", grouping_strategy="cause-chain-v1")
    reporter._send = lambda event: received.append(event) or True
    try:
        try:
            raise ValueError("root")
        except ValueError as root:
            try:
                raise RuntimeError("wrapper") from root
            except RuntimeError as wrapper:
                reporter.capture_exception(wrapper)
        suppressed = RuntimeError("suppressed")
        suppressed.__context__ = ValueError("hidden")
        suppressed.__suppress_context__ = True
        reporter.capture_exception(suppressed)
        cycle = RuntimeError("cycle")
        cycle.__cause__ = cycle
        reporter.capture_exception(cycle)
        reporter.capture_exception(ExceptionGroup("aggregate", [ValueError("one"), TypeError("two")]))
        deep = ValueError("deep")
        for _ in range(10):
            outer = RuntimeError("wrapper")
            outer.__cause__ = deep
            deep = outer
        reporter.capture_exception(deep)
        assert reporter.close()
        assert received[0]["causes"][0]["exception_type"] == "builtins.ValueError"
        assert "ValueError: root" not in received[0]["stack_trace"]
        assert (
            received[0]["causes"][0]["frames"][-1]["function"]
            == "test_cause_capture_follows_python_semantics_and_bounds_cycles"
        )
        assert received[1]["causes"] == []
        assert "causes_incomplete" not in received[1]
        assert all(event["causes_incomplete"] for event in received[2:])
        assert len(received[4]["causes"]) == 8
    finally:
        reporter.close()


def test_cause_models_round_trip_selected_origin():
    from faas_sdk.preview.models.issue_event import IssueEvent
    from faas_sdk.preview.models.issue_grouping_explanation import IssueGroupingExplanation

    payload = {
        "event_id": "11111111-1111-4111-8111-111111111111",
        "occurred_at": "2026-10-11T12:00:00Z",
        "exception_type": "Wrapper",
        "message": "wrapped",
        "grouping_strategy": "cause-chain-v1",
        "causes": [
            {
                "exception_type": "Inner",
                "message": "failed",
                "error_code": "DOWN",
                "frames": [],
                "frame_order": "oldest-first",
            }
        ],
        "causes_incomplete": False,
    }
    event = IssueEvent.from_dict(payload)
    assert event.causes[0].error_code == "DOWN"
    assert event.to_dict()["causes"] == payload["causes"]
    exp = IssueGroupingExplanation.from_dict(
        {
            "grouping_version": 3,
            "strategy": "cause-chain-v1",
            "basis": "fallback",
            "source_kind": "exception",
            "exception_type": "Inner",
            "discriminator_kind": "error-code",
            "cause_index": 0,
        }
    )
    assert exp.to_dict()["cause_index"] == 0


def test_priority_preview_and_atomic_undo_sdk_contract():
    from uuid import UUID

    import httpx

    from faas_sdk.preview.api.apps.undo_issue_split_rule import sync
    from faas_sdk.client import AuthenticatedClient
    from faas_sdk.preview.models.issue_split_preview import IssueSplitPreview
    from faas_sdk.preview.models.issue_split_rule_undo_request import IssueSplitRuleUndoRequest

    source = "11111111-1111-4111-8111-111111111111"
    rule_id = "22222222-2222-4222-8222-222222222222"
    target = "33333333-3333-4333-8333-333333333333"
    preview = {
        "original_issue_id": source,
        "canonical_issue_id": source,
        "matcher": {"route": "/checkout"},
        "lifetime_event_count": 4,
        "retained_event_count": 3,
        "matching_event_count": 2,
        "nonmatching_event_count": 1,
        "unavailable_history_event_count": 1,
        "matching_event_ids": [target],
        "eligible_event_count": 1,
        "shadowed_event_count": 1,
        "eligible_event_ids": [target],
        "application_status": "new-rule",
    }
    parsed = IssueSplitPreview.from_dict(preview)
    assert parsed.eligible_event_count == 1
    assert parsed.shadowed_event_count == 1
    assert parsed.to_dict()["application_status"] == "new-rule"

    def handle(request):
        assert request.method == "POST"
        assert request.url.path == f"/v1/apps/payments/issues/{source}/split-rules/{rule_id}/undo"
        assert json.loads(request.content) == {"canonical_source_issue_id": source}
        return httpx.Response(
            200,
            json={
                "id": rule_id,
                "source_issue_id": source,
                "target_issue_id": target,
                "priority": 1,
                "matcher": {"route": "/checkout"},
                "enabled": False,
                "initial_moved_event_count": 1,
                "unavailable_history_event_count": 1,
                "created_at": "2026-10-11T12:00:00Z",
                "updated_at": "2026-10-11T12:01:00Z",
            },
        )

    client = AuthenticatedClient(base_url="https://api.gregale.dev", token="test").set_httpx_client(
        httpx.Client(base_url="https://api.gregale.dev", transport=httpx.MockTransport(handle))
    )
    result = sync(
        "payments",
        UUID(source),
        UUID(rule_id),
        client=client,
        body=IssueSplitRuleUndoRequest(canonical_source_issue_id=UUID(source)),
    )
    assert result.enabled is False
    assert str(result.target_issue_id) == target


def test_route_does_not_change_trimmed_cause_evidence():
    from copy import deepcopy
    from faas_sdk.preview.issues import _trim_event

    frame = {"file": "x" * 413, "function": "f" * 150, "line": 1, "in_app": True}
    event = {"frames": [frame] * 32, "causes": [
        {"exception_type": "Error", "frames": [frame] * 32},
        {"exception_type": "Error", "frames": [frame] * 32},
    ]}
    without_route, with_route = deepcopy(event), deepcopy(event)
    with_route["route"] = "\x01" * 256
    assert _trim_event(without_route) and _trim_event(with_route)
    assert without_route["causes"] == with_route["causes"]
    assert without_route.get("causes_incomplete") == with_route.get("causes_incomplete")
    assert without_route["frames"] == with_route["frames"]
