"""Guard against silently omitted request models and successful response types."""

import httpx

from faas_sdk.api.orgs import get_application_standard_version, publish_application_standard_version
from faas_sdk.client import Client
from faas_sdk.models.application_standard_version import ApplicationStandardVersion
from faas_sdk.models.create_application_standard_version_request import CreateApplicationStandardVersionRequest


def test_standard_sdk_retains_all_requirement_types_and_parses_success():
    resource = "00000000-0000-4000-8000-000000000001"
    definition = {
        "log_destinations": {"mode": "mandatory", "override": "extend", "value": [resource]},
        "require_signed": {"mode": "mandatory", "value": True},
        "security_policy": {"mode": "mandatory", "override": "narrow", "value": "enforce"},
        "trusted_publishers": {"mode": "restricted", "value": [resource]},
        "egress_cidrs": {"mode": "restricted", "value": ["203.0.113.0/24"]},
        "egress_extra_ports": {"mode": "restricted", "value": [5432]},
    }
    payload = {"expected_version": 0, "definition": definition, "description": "Production"}
    request = CreateApplicationStandardVersionRequest.from_dict(payload)
    kwargs = publish_application_standard_version._get_kwargs("acme", "baseline", body=request)
    assert kwargs["method"] == "post"
    assert kwargs["url"] == "/v1/orgs/acme/application-standards/baseline/versions"
    assert kwargs["json"] == payload

    version = {
        "standard_id": resource,
        "org_id": resource,
        "slug": "baseline",
        "version": 1,
        "definition": definition,
        "definition_hash": "a" * 64,
        "description": "Production",
        "created_by": resource,
        "created_at": "2026-09-30T12:00:00Z",
    }
    client = Client(base_url="https://api.example.test", raise_on_unexpected_status=True)
    for endpoint, status in [(publish_application_standard_version, 201), (get_application_standard_version, 200)]:
        parsed = endpoint._parse_response(client=client, response=httpx.Response(status, json=version))
        assert isinstance(parsed, ApplicationStandardVersion)
        assert parsed.version == 1
        assert parsed.definition.to_dict() == definition
