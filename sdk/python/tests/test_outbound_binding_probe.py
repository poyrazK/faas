from uuid import UUID

import httpx

from faas_sdk.api.outbound import (
    delete_outbound_binding_probe_policy,
    get_outbound_binding_probe_policy,
    set_outbound_binding_probe_policy,
)
from faas_sdk.client import Client
from faas_sdk.models.app_binding_inventory_item import AppBindingInventoryItem
from faas_sdk.models.outbound_binding_probe_policy import OutboundBindingProbePolicy
from faas_sdk.models.problem import Problem


def test_outbound_probe_policy_wire_and_inventory_roundtrip() -> None:
    integration = UUID("01234567-89ab-cdef-0123-456789abcdef")
    policy = OutboundBindingProbePolicy(method="HEAD", path="/health", expected_status=204)
    path = f"/v1/outbound/integrations/{integration}/probe-policy"
    put = set_outbound_binding_probe_policy._get_kwargs(integration, body=policy)
    assert (put["method"], put["url"], put["json"]) == ("put", path, policy.to_dict())
    assert get_outbound_binding_probe_policy._get_kwargs(integration)["url"] == path
    assert delete_outbound_binding_probe_policy._get_kwargs(integration)["method"] == "delete"
    client = Client(base_url="https://example.test")
    parsed = get_outbound_binding_probe_policy._parse_response(
        client=client, response=httpx.Response(200, json=policy.to_dict())
    )
    assert isinstance(parsed, OutboundBindingProbePolicy)
    assert parsed.to_dict() == policy.to_dict()
    problem = set_outbound_binding_probe_policy._parse_response(
        client=client,
        response=httpx.Response(400, json={"title": "Invalid probe", "status": 400, "code": "validation_error"}),
    )
    assert isinstance(problem, Problem)
    assert problem.status == 400
    item = AppBindingInventoryItem.from_dict(
        {
            "type": "outbound",
            "name": "provider",
            "binding": "",
            "scope": "app",
            "access": "managed",
            "state": "enabled",
            "runtime_status": "not_observed",
            "verification_status": "unknown",
            "outbound_probe": policy.to_dict(),
        }
    )
    assert item.to_dict()["outbound_probe"] == policy.to_dict()
