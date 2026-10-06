import json

import httpx

from faas_sdk.api.workflows import get_automation_revision, list_automation_revisions, restore_automation_revision
from faas_sdk.client import AuthenticatedClient
from faas_sdk.models.restore_automation_revision_request import RestoreAutomationRevisionRequest


def test_revision_history_and_restore_requests():
    calls = []
    revision = {
        "version": 42,
        "definition": {"name": "paid-invoice", "steps": []},
        "definition_hash": "a" * 64,
        "recorded_at": "2026-10-04T12:00:00Z",
        "legacy_snapshot": False,
        "published_by_account_id": "00000000-0000-0000-0000-000000000001",
    }

    def respond(request: httpx.Request) -> httpx.Response:
        calls.append(request)
        assert request.headers["Authorization"] == "Bearer test-token"
        if request.method == "GET" and request.url.path.endswith("/revisions"):
            assert request.url.params["limit"] == "10"
            assert request.url.params["offset"] == "0"
            return httpx.Response(200, json={"revisions": [revision], "total": 1, "limit": 10, "offset": 0})
        if request.method == "GET" and request.url.path.endswith("/revisions/42"):
            return httpx.Response(200, json=revision)
        if request.method == "POST" and request.url.path.endswith("/revisions/42/restore"):
            assert request.headers["Idempotency-Key"] == "restore-v42"
            assert json.loads(request.content) == {"expected_version": 47}
            return httpx.Response(
                200,
                json={
                    "name": "paid-invoice",
                    "version": 48,
                    "source": "dashboard",
                    "draft": {"name": "paid-invoice", "steps": []},
                    "enabled": True,
                },
            )
        raise AssertionError(f"unexpected request {request.method} {request.url}")

    with AuthenticatedClient(
        base_url="https://example.test", token="test-token", httpx_args={"transport": httpx.MockTransport(respond)}
    ) as client:
        page = list_automation_revisions.sync("billing", "paid-invoice", client=client, limit=10)
        assert page is not None and page.total == 1 and page.revisions[0].version == 42
        got = get_automation_revision.sync("billing", "paid-invoice", 42, client=client)
        assert got is not None and got.definition_hash == "a" * 64
        restored = restore_automation_revision.sync(
            "billing",
            "paid-invoice",
            42,
            client=client,
            body=RestoreAutomationRevisionRequest(expected_version=47),
            idempotency_key="restore-v42",
        )
        assert restored is not None and restored.version == 48 and restored.draft.name == "paid-invoice"

    assert len(calls) == 3
