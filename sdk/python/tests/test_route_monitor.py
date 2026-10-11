import json
from pathlib import Path
from uuid import UUID

import httpx

from faas_sdk import FaaSClient
from faas_sdk.api.apps import (
    get_route_monitor,
    get_route_monitor_incident,
    get_route_monitor_report,
    list_route_monitor_incidents,
    preview_route_monitor,
    set_route_monitor,
)
from faas_sdk.models import (
    PreviewRouteMonitorRequest,
    RouteMonitorConfig,
    RouteMonitorIncident,
    RouteMonitorPreview,
    RouteMonitorWebhookPayload,
    SetRouteMonitorRequest,
)


def test_route_monitor_escalation_webhook_payload_round_trip():
    payload = {
        "version": 1,
        "app_id": "11111111-1111-4111-8111-111111111111",
        "deployment_id": "22222222-2222-4222-8222-222222222222",
        "incident_id": "33333333-3333-4333-8333-333333333333",
        "transition_id": "44444444-4444-4444-8444-444444444444",
        "revision": 7,
        "status": "open",
        "checked_at": "2026-10-06T09:01:00+00:00",
        "incident_path": "/v1/apps/demo/route-monitor/incidents/33333333-3333-4333-8333-333333333333",
        "customer_impact": {
            "group_by": "tenant",
            "coverage": "observed_only",
            "observed_customers": 5,
            "violated_customers": 2,
            "unknown_customers": 1,
        },
        "escalation": {
            "previous_checked_at": "2026-10-06T09:00:00+00:00",
            "newly_violated_routes": 1,
            "newly_violated_signals": 2,
        },
    }
    assert RouteMonitorWebhookPayload.from_dict(payload).to_dict() == payload


def test_production_route_monitor_preserves_budget_and_saved_evidence():
    payload = json.loads(
        (Path(__file__).resolve().parents[3] / "tests/fixtures/production-route-incident.json").read_text()
    )
    assert RouteMonitorIncident.from_dict(payload).to_dict() == payload
    timeline_payload = {
        **payload,
        "baseline": {
            "deployment_id": "66666666-6666-4666-8666-666666666666",
            "commit_sha": "b" * 40,
            "repository": "github.com/team/service",
            "source_root": ".",
        },
        "timeline": [
            {
                "checked_at": payload["opened_at"],
                "coverage": "observed_only",
                "status": "violated",
                "reason": "sustained_budget_violation",
                "customer_impact": {
                    "group_by": "tenant",
                    "coverage": "observed_only",
                    "observed_customers": 5,
                    "violated_customers": 2,
                    "unknown_customers": 1,
                },
                "routes": [
                    {
                        "route_index": 0,
                        "status": "violated",
                        "error_status": "violated",
                        "latency_status": "healthy",
                        "customer_impact": {
                            "group_by": "tenant",
                            "coverage": "observed_only",
                            "observed_customers": 3,
                            "violated_customers": 2,
                            "unknown_customers": 1,
                        },
                    }
                ],
            }
        ],
        "timeline_truncated": False,
        "escalations": [
            {
                "transition_id": "44444444-4444-4444-8444-444444444444",
                "checked_at": "2026-10-03T13:05:04.746212+00:00",
                "previous_checked_at": "2026-10-03T13:04:04.746212+00:00",
                "newly_violated_routes": 1,
                "newly_violated_signals": 1,
                "signals": [{"route_index": 0, "signal": "latency", "finding": payload["opening_report"]["routes"][0]}],
                "evidence": payload["evidence"][:1],
                "evidence_truncated": False,
            }
        ],
        "escalations_truncated": False,
    }
    assert RouteMonitorIncident.from_dict(timeline_payload).to_dict() == timeline_payload
    config = {
        "app_id": payload["app_id"],
        "enabled": True,
        "revision": 1,
        "routes": [{"method": "POST", "path": "/checkout", "max_5xx_rate_bps": 0, "max_p95_ms": 300}],
    }
    preview_payload = {
        "current_revision": 1,
        "preview_only": True,
        "config_change_resets_observation_anchor": True,
        "report": payload["opening_report"],
    }
    calls = []

    def respond(req: httpx.Request) -> httpx.Response:
        calls.append(req)
        assert req.url.path.startswith("/v1/apps/demo/route-monitor")
        if req.method == "PUT":
            assert json.loads(req.content) == {"enabled": True, "expected_revision": 0, "routes": config["routes"]}
            result = config
        elif req.method == "POST" and req.url.path.endswith("/preview"):
            assert json.loads(req.content) == {"routes": config["routes"]}
            result = preview_payload
        elif req.url.path.endswith("/report"):
            result = payload["opening_report"]
        elif req.url.path.endswith("/incidents/" + payload["id"]):
            result = payload
        elif req.url.path.endswith("/incidents"):
            assert req.url.params["limit"] == "5"
            assert req.url.params["before"] == payload["id"]
            result = {"app_id": payload["app_id"], "incidents": [payload]}
        else:
            result = config
        return httpx.Response(200, json=result)

    client = FaaSClient(
        base_url="https://example.test", token="test", httpx_args={"transport": httpx.MockTransport(respond)}
    )
    with client.httpx_client:
        assert get_route_monitor.sync("demo", client=client.inner).to_dict() == config
        request = SetRouteMonitorRequest.from_dict(
            {"enabled": True, "expected_revision": 0, "routes": config["routes"]}
        )
        assert set_route_monitor.sync("demo", client=client.inner, body=request).to_dict() == config
        proposed = PreviewRouteMonitorRequest.from_dict({"routes": config["routes"]})
        preview = preview_route_monitor.sync("demo", client=client.inner, body=proposed)
        assert preview == RouteMonitorPreview.from_dict(preview_payload)
        assert preview.report.revision == preview.current_revision == 1
        assert get_route_monitor_report.sync("demo", client=client.inner).to_dict() == payload["opening_report"]
        assert list_route_monitor_incidents.sync(
            "demo", client=client.inner, limit=5, before=UUID(payload["id"])
        ).to_dict() == {"app_id": payload["app_id"], "incidents": [payload]}
        assert get_route_monitor_incident.sync("demo", UUID(payload["id"]), client=client.inner).to_dict() == payload
        assert len(calls) == 6
        assert RouteMonitorConfig.from_dict(config).routes[0].max_5xx_rate_bps == 0
