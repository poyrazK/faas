"""Shared SDK fixtures and declared field coverage, independent of codegen."""

import importlib
import json
import re
from pathlib import Path
from typing import get_args

import attrs
import httpx
import pytest

from faas_sdk.api.apps import create_app, get_app, update_app
from faas_sdk.client import AuthenticatedClient
from faas_sdk.models.app_response import AppResponse
from faas_sdk.models.create_app_request import CreateAppRequest
from faas_sdk.models.update_app_request import UpdateAppRequest

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
