from faas_sdk.models.event_preview_subscription import EventPreviewSubscription
from faas_sdk.models.workflow_trigger_spec import WorkflowTriggerSpec


def test_event_trigger_preserves_content_filter():
    wire = {
        "type": "event",
        "source": "billing.*",
        "event_type": "invoice.paid",
        "filter": {"data": {"amount": {"$gt": 100}}},
        "enabled": True,
    }
    assert WorkflowTriggerSpec.from_dict(wire).to_dict() == wire


def test_preview_identifies_workflow_recipient():
    wire = {
        "app_slug": "billing",
        "subscription_id": "6320f18f-c7e5-4bac-91ef-5f1ce6d87845",
        "source": "billing.*",
        "type": "invoice.paid",
        "filter": {},
        "reason": "would_deliver",
        "workflow_name": "paid_invoice",
        "deployment_id": "ba1dd633-4b69-4328-a920-3bf28b2b5c2d",
    }
    assert EventPreviewSubscription.from_dict(wire).to_dict() == wire
