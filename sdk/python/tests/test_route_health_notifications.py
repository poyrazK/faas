from uuid import UUID

from faas_sdk.models import CreateAppWebhookRequest, RouteHealthTransitionWebhookPayload, UpdateAppWebhookRequest


def test_route_health_notification_types_and_subscription_events():
    app = "00000000-0000-4000-8000-000000000001"
    candidate = "00000000-0000-4000-8000-000000000002"
    decision = "00000000-0000-4000-8000-000000000004"
    blocked = "00000000-0000-4000-8000-000000000005"
    payload = {
        "version": 1,
        "app_id": app,
        "deployment_id": candidate,
        "stable_deployment_id": "stable",
        "decision_id": decision,
        "blocked_decision_id": blocked,
        "status": "resumed",
        "health_status": "healthy",
        "reason": "selected_routes_healthy",
        "source": "worker",
        "canary_step": 0,
        "revision": 1,
        "observation_anchor": "2026-10-02T18:00:00+00:00",
        "checked_at": "2026-10-02T19:00:00+00:00",
        "previous_traffic_percent": 1,
        "requested_traffic_percent": 10,
        "history_path": f"/v1/apps/demo/route-health/deployments/{candidate}/history/{decision}",
    }
    got = RouteHealthTransitionWebhookPayload.from_dict(payload)
    assert got.decision_id == UUID(decision)
    assert got.blocked_decision_id == UUID(blocked)
    assert got.to_dict() == payload
    abort = {**payload, "status": "aborted", "health_status": "regressed", "requested_traffic_percent": 0}
    assert RouteHealthTransitionWebhookPayload.from_dict(abort).to_dict() == abort
    events = ["routes.health.blocked", "routes.health.resumed", "routes.health.aborted"]
    create = {
        "target_url": "https://example.test/hooks",
        "webhook_secret": "tests-only-secret-with-at-least-32-bytes",
        "event_filter": events,
    }
    assert CreateAppWebhookRequest.from_dict(create).to_dict() == create
    update = {"event_filter": events}
    assert UpdateAppWebhookRequest.from_dict(update).to_dict() == update
