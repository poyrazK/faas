"""ADR-20261017233500: selected app IDs stay repeated query parameters."""
from uuid import UUID

import httpx

from faas_sdk.preview.api.account import get_selected_app_trace
from faas_sdk.client import Client
from faas_sdk.preview.models import SelectedTraceLookupResponse


def test_selected_trace_wire_partial_and_missing_source_time():
    trace = "4bf92f3577b34da6a3ce929d0e0e4736"
    ids = [UUID("11111111-1111-4111-8111-111111111111"), UUID("22222222-2222-4222-8222-222222222222")]
    kwargs = get_selected_app_trace._get_kwargs(trace, app_id=ids, limit=7)
    request = httpx.Request(kwargs["method"], "https://api.invalid" + kwargs["url"], params=kwargs["params"])
    assert request.url.path == f"/v1/account/traces/{trace}/selected-apps"
    assert request.url.params.get_list("app_id") == [str(value) for value in ids]
    assert request.url.params["limit"] == "7"
    wire = {
        "selection": "explicit_app_ids_v1", "trace_id": trace,
        "apps": [{"id": str(ids[0]), "slug": "alpha"}],
        "generated_at": "2026-10-17T11:00:00Z", "limit": 7,
        "request_window_start": "2026-10-14T11:00:00Z", "request_window_end": "2026-10-17T11:00:00Z", "request_retention_days": 3,
        "requests": [], "requests_status": "unavailable", "requests_truncated": False,
        "invocations": [{"app_id": str(ids[0]), "id": "33333333-3333-4333-8333-333333333333", "source": "queue", "state": "pending", "attempts": 0, "created_at": "2020-10-17T10:00:00Z"}],
        "invocations_status": "ok", "invocations_truncated": False, "partial": True,
    }
    parsed = get_selected_app_trace._parse_response(client=Client(base_url="https://api.invalid"), response=httpx.Response(200, json=wire))
    assert isinstance(parsed, SelectedTraceLookupResponse)
    round_trip = parsed.to_dict()
    assert round_trip["partial"] and round_trip["requests_status"] == "unavailable"
    assert "started_at" not in round_trip["invocations"][0]
    assert "headers" not in round_trip["invocations"][0]
    problem = get_selected_app_trace._parse_response(client=Client(base_url="https://api.invalid"), response=httpx.Response(404, json={"type": "about:blank", "title": "Not found", "status": 404, "code": "not_found"}))
    assert problem.code == "not_found"
