"""Shared SDK fixtures and declared field coverage, independent of codegen."""

import importlib
import json
import re
from pathlib import Path
from typing import get_args
from uuid import UUID

import attrs
import httpx
import pytest

from faas_sdk.api.apps import create_app, explain_app_service_binding, get_app, update_app
from faas_sdk.client import AuthenticatedClient
from faas_sdk.models.app_response import AppResponse
from faas_sdk.models.create_app_request import CreateAppRequest
from faas_sdk.models.service_binding_explanation import ServiceBindingExplanation
from faas_sdk.models.update_app_request import UpdateAppRequest


def test_managed_service_receipt_context_is_typed():
    from faas_sdk.models.binding_verification import BindingVerification
    from faas_sdk.models.managed_service_probe_spec import ManagedServiceProbeSpec

    context = {
        "version": 1,
        "caller_deployment_id": "11111111-1111-4111-8111-111111111111",
        "scope": "production",
        "service": "billing",
        "binding": "CUSTOM_BILLING_URL",
        "target_app_id": "22222222-2222-4222-8222-222222222222",
        "target_deployment_id": "33333333-3333-4333-8333-333333333333",
        "release_id": "44444444-4444-4444-8444-444444444444",
        "context_revision": "a" * 64,
    }
    receipt = BindingVerification.from_dict(
        {
            "result": "passed",
            "source": "task_guest",
            "scope": "production",
            "deployment_id": context["caller_deployment_id"],
            "managed_service": context,
        }
    )
    assert isinstance(receipt.managed_service, ManagedServiceProbeSpec)
    assert receipt.managed_service.binding == "CUSTOM_BILLING_URL"
    assert receipt.to_dict()["managed_service"] == context
    assert "managed_service" not in receipt.additional_properties


def test_managed_service_inventory_preserves_frozen_context():
    from faas_sdk.api.apps import get_app_binding_inventory
    from faas_sdk.models.app_binding_inventory import AppBindingInventory

    caller = UUID("11111111-1111-4111-8111-111111111111")
    target = UUID("22222222-2222-4222-8222-222222222222")
    release = UUID("33333333-3333-4333-8333-333333333333")

    def handle(request):
        assert request.method == "GET"
        assert request.url.params["deployment_id"] == str(caller)
        return httpx.Response(
            200,
            json={
                "app": "frontend",
                "service_binding_source": "environment_release",
                "generated_at": "2026-10-17T10:00:00Z",
                "complete": True,
                "requested_deployment_id": str(caller),
                "verification_deployment_id": str(caller),
                "verification_scope": "production",
                "bindings": [
                    {
                        "type": "service",
                        "name": "billing",
                        "binding": "BILLING_URL",
                        "scope": "production",
                        "access": "invoke",
                        "state": "enforced",
                        "runtime_status": "unknown",
                        "verification_status": "unsupported",
                        "transport": "https",
                        "service_target_deployment_id": str(target),
                        "service_context": {
                            "source": "environment_release",
                            "caller_deployment_id": str(caller),
                            "target_app": "git-billing",
                            "release_id": str(release),
                            "revision": "a" * 64,
                            "routing_status": "resolved",
                            "routing_reason": "active_reviewed_release",
                            "verification_supported": False,
                            "verification_reason": "managed_service_verification_unsupported",
                        },
                    }
                ],
            },
        )

    with httpx.Client(base_url="https://api.invalid", transport=httpx.MockTransport(handle)) as transport:
        client = AuthenticatedClient(base_url="https://api.invalid", token="fixture").set_httpx_client(transport)
        out = get_app_binding_inventory.sync("frontend", client=client, deployment_id=caller)
    assert isinstance(out, AppBindingInventory)
    assert out.service_binding_source == "environment_release"
    assert "service_binding_source" not in out.additional_properties
    item = out.bindings[0]
    assert item.service_target_deployment_id == target
    assert item.service_context.target_app == "git-billing"
    assert item.service_context.release_id == release
    assert item.service_context.verification_supported is False
    assert "service_context" not in item.additional_properties


def test_deployment_service_explanation_wire():
    deployment = UUID("11111111-1111-4111-8111-111111111111")
    target = UUID("22222222-2222-4222-8222-222222222222")
    release = UUID("33333333-3333-4333-8333-333333333333")
    calls = []

    def handle(request):
        calls.append(request)
        assert request.method == "GET"
        assert request.url.path == "/v1/apps/frontend/bindings/services/billing/explanation"
        assert request.url.params["deployment_id"] == str(deployment)
        assert request.url.params["scope"] == "staging"
        assert request.url.params["method"] == "GET"
        assert request.url.params["path"] == "/invoices"
        return httpx.Response(
            200,
            json={
                "caller": "frontend",
                "target": "billing",
                "scope": "staging",
                "method": "GET",
                "path": "/invoices",
                "result": "denied",
                "checked_at": "2026-10-17T09:00:00Z",
                "limitations": "Metadata observation only.",
                "caller_deployment_id": str(deployment),
                "target_deployment_id": str(target),
                "release_id": str(release),
                "policy_source": "environment_release",
                "canonical_transport": "https",
                "checks": [{"rule": "inbound_scopes", "result": "denied", "reason": "method_path_not_granted"}],
            },
        )

    with httpx.Client(base_url="https://api.invalid", transport=httpx.MockTransport(handle)) as transport:
        client = AuthenticatedClient(base_url="https://api.invalid", token="fixture").set_httpx_client(transport)
        out = explain_app_service_binding.sync(
            "frontend",
            "billing",
            client=client,
            deployment_id=deployment,
            scope="staging",
            method="GET",
            path="/invoices",
        )
    assert len(calls) == 1
    assert isinstance(out, ServiceBindingExplanation)
    assert out.result == "denied"
    assert out.policy_source == "environment_release"
    assert out.caller_deployment_id == deployment
    assert out.target_deployment_id == target
    assert out.release_id == release
    assert out.checks[0].reason == "method_path_not_granted"


CONTRACTS = Path(__file__).resolve().parents[2] / "service-binding-contracts"
SCHEMAS = json.loads((CONTRACTS / "schema.json").read_text())["schemas"]
CASES = json.loads((CONTRACTS / "cases.json").read_text())


@pytest.mark.parametrize("name,schema", SCHEMAS.items())
def test_declared_fields(name, schema):
    module_name = re.sub(r"(?<!^)(?=[A-Z])", "_", name).lower()
    model = getattr(importlib.import_module(f"faas_sdk.models.{module_name}"), name)
    if "enum" in schema:
        assert list(get_args(model)) == schema["enum"]
        return
    # Unknown fields otherwise survive through additional_properties, hiding
    # missing typed SDK fields from a simple JSON round trip.
    fields = attrs.fields_dict(model)
    for key in schema.get("properties", {}):
        assert key in fields, f"{name} must declare {key}"
        assert (fields[key].default is attrs.NOTHING) == (key in schema.get("required", []))


def project(schema, value):
    if value is None:
        return value
    if "$ref" in schema:
        return project(SCHEMAS[schema["$ref"].rsplit("/", 1)[-1]], value)
    if "properties" in schema:
        return {key: project(prop, value[key]) for key, prop in schema["properties"].items() if key in value}
    return value


@pytest.mark.parametrize("fixture", CASES["requests"], ids=lambda fixture: fixture["name"])
def test_shared_request_wire(fixture):
    create = fixture["schema"] == "CreateAppRequest"
    assert create or fixture["schema"] == "UpdateAppRequest"
    schema = SCHEMAS[fixture["schema"]]
    calls = []

    def handle(request):
        calls.append(request)
        assert request.method == ("POST" if create else "PATCH")
        assert request.url.path == ("/v1/apps" if create else "/v1/apps/demo")
        assert project(schema, json.loads(request.content)) == project(schema, fixture["body"])
        return httpx.Response(201 if create else 200, json=CASES["responses"][1]["body"])

    with httpx.Client(base_url="https://api.invalid", transport=httpx.MockTransport(handle)) as transport:
        client = AuthenticatedClient(base_url="https://api.invalid", token="fixture").set_httpx_client(transport)
        response = (
            create_app.sync_detailed(client=client, body=CreateAppRequest.from_dict(fixture["body"]))
            if create
            else update_app.sync_detailed("demo", client=client, body=UpdateAppRequest.from_dict(fixture["body"]))
        )
    assert len(calls) == 1
    assert isinstance(response.parsed, AppResponse)
    assert response.parsed.manifest.service_binding_transport == "https"
    assert "service_binding_transport" not in response.parsed.manifest.additional_properties


@pytest.mark.parametrize("fixture", CASES["responses"], ids=lambda fixture: fixture["name"])
def test_shared_response_wire(fixture):
    def handle(request):
        assert request.method == "GET"
        assert request.url.path == "/v1/apps/demo"
        return httpx.Response(200, json=fixture["body"])

    with httpx.Client(base_url="https://api.invalid", transport=httpx.MockTransport(handle)) as transport:
        client = AuthenticatedClient(base_url="https://api.invalid", token="fixture").set_httpx_client(transport)
        app = get_app.sync("demo", client=client)
    assert isinstance(app, AppResponse)
    assert project(SCHEMAS["AppResponse"], app.to_dict()) == project(SCHEMAS["AppResponse"], fixture["body"])
