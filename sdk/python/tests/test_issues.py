import json
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

from faas_sdk.issues import IssueReporter


def test_regrouping_models_round_trip_actions_and_original_identity():
    from faas_sdk.models.issue_action_request import IssueActionRequest
    from faas_sdk.models.issue_activity import IssueActivity
    from faas_sdk.models.issue_detail import IssueDetail

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

    from faas_sdk.api.apps import preview_issue_split
    from faas_sdk.client import AuthenticatedClient
    from faas_sdk.models.issue_split_matcher import IssueSplitMatcher
    from faas_sdk.models.issue_split_preview import IssueSplitPreview

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

    from faas_sdk.api.apps import create_issue_split_rule, update_issue_split_rule
    from faas_sdk.client import AuthenticatedClient
    from faas_sdk.models.issue_split_matcher import IssueSplitMatcher
    from faas_sdk.models.issue_split_rule import IssueSplitRule
    from faas_sdk.models.issue_split_rule_update import IssueSplitRuleUpdate

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
    from faas_sdk.models.issue_event import IssueEvent
    from faas_sdk.models.issue_grouping_explanation import IssueGroupingExplanation

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

    from faas_sdk.api.apps.undo_issue_split_rule import sync
    from faas_sdk.client import AuthenticatedClient
    from faas_sdk.models.issue_split_preview import IssueSplitPreview
    from faas_sdk.models.issue_split_rule_undo_request import IssueSplitRuleUndoRequest

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
