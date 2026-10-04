import json

import httpx

from faas_sdk.api.workflows import delete_automation, publish_automation, save_automation_draft
from faas_sdk.client import AuthenticatedClient
from faas_sdk.models.publish_automation_request import PublishAutomationRequest
from faas_sdk.models.save_automation_draft_request import SaveAutomationDraftRequest
from faas_sdk.models.workflow_spec import WorkflowSpec


def test_authoring_requests_preserve_revision_ownership_and_draft():
    definition = {
        "name": "invoice",
        "trigger": {"type": "event", "source": "billing", "event_type": "invoice.paid"},
        "steps": [{"name": "record", "path": "/record", "input": {"id": "{{input.data.id}}"}}],
    }
    calls = []

    def respond(request: httpx.Request) -> httpx.Response:
        calls.append(request)
        assert request.headers["Authorization"] == "Bearer test-token"
        assert request.url.path.startswith("/v1/apps/billing/automations/invoice")
        if request.method == "PUT":
            assert json.loads(request.content) == {"expected_version": 7, "definition": definition}
        elif request.method == "POST":
            assert json.loads(request.content) == {"expected_version": 8, "take_over_manifest": True}
        else:
            assert request.url.params["expected_version"] == "9"
            assert request.url.params["restore_manifest"] == "true"
            return httpx.Response(204)
        return httpx.Response(
            200, json={"name": "invoice", "version": 8, "source": "draft", "draft": definition, "enabled": True}
        )

    with AuthenticatedClient(
        base_url="https://example.test", token="test-token", httpx_args={"transport": httpx.MockTransport(respond)}
    ) as client:
        saved = save_automation_draft.sync(
            "billing",
            "invoice",
            client=client,
            body=SaveAutomationDraftRequest(expected_version=7, definition=WorkflowSpec.from_dict(definition)),
        )
        assert saved is not None
        assert saved.draft.to_dict() == definition
        publish_automation.sync(
            "billing",
            "invoice",
            client=client,
            body=PublishAutomationRequest(expected_version=8, take_over_manifest=True),
        )
        delete_automation.sync("billing", "invoice", client=client, expected_version=9, restore_manifest=True)
    assert len(calls) == 3


def test_outbound_step_roundtrip_preserves_integration_and_templates():
    definition = {
        "name": "crm",
        "steps": [
            {
                "name": "contact",
                "outbound": {
                    "integration_id": "00000000-0000-0000-0000-000000000001",
                    "method": "POST",
                    "path": "/v1/contacts",
                    "idempotency_supported": True,
                },
                "input": {"email": "{{input.email}}"},
                "retry": {"max_attempts": 3, "backoff": "exponential"},
            }
        ],
    }
    model = WorkflowSpec.from_dict(definition)
    assert str(model.steps[0].outbound.integration_id) == "00000000-0000-0000-0000-000000000001"
    assert model.to_dict() == definition
