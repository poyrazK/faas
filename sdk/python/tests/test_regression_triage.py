"""ADR-20261017210000: exact review keys, guard serialization and workflow receipts."""
from uuid import UUID

import httpx

from faas_sdk.api.apps import get_app_debug_regression_review, apply_app_debug_regression_action
from faas_sdk.client import Client
from faas_sdk.models import GuardedDebugRegressionActionRequest, DebugRegressionReview


def test_guarded_triage_wire_and_stale_error():
    app, deployment, version = ("11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222", "33333333-3333-4333-8333-333333333333")
    route = "GET /orders + items"
    kwargs = get_app_debug_regression_review._get_kwargs("alpha", deployment_id=UUID(deployment), route=route)
    assert kwargs["params"] == {"deployment_id": deployment, "route": route}
    request = GuardedDebugRegressionActionRequest.from_dict({"deployment_id": deployment, "route": route, "action": "dismiss", "expected_app_id": app, "expected_version": version, "reason": "Investigating", "dismissed_until": "2026-10-18T10:00:00Z"})
    wire = request.to_dict()
    assert wire["expected_version"] == version and wire["expected_app_id"] == app
    assert wire["route"] == route and wire["reason"] == "Investigating"
    review = DebugRegressionReview.from_dict({"app_id": app, "deployment_id": deployment, "route": route, "version": version, "state": "dismissed", "first_detected_at": "2026-10-17T10:00:00Z", "last_detected_at": "2026-10-17T11:00:00Z", "dismissed_until": "2026-10-18T10:00:00Z"})
    assert DebugRegressionReview.from_dict(review.to_dict()).version == UUID(version)
    problem = apply_app_debug_regression_action._parse_response(client=Client(base_url="http://localhost"), response=httpx.Response(409, json={"type": "about:blank", "title": "Changed", "status": 409, "code": "regression_changed", "detail": "review again"}))
    assert problem.code == "regression_changed"
