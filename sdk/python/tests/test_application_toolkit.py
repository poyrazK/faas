import asyncio
import json
from pathlib import Path

import pytest
from faas_sdk.preview import ApplicationToolkit, ApplicationToolkitError, ToolkitAccess, ToolkitMount

FIXTURE = json.loads((Path(__file__).resolve().parents[2] / "request-contracts/application-toolkit.json").read_text())
URI = FIXTURE["uri_prefix"] + "owned"
TEMPLATE = FIXTURE["uri_prefix"] + "{id}"


class Adapter:
    tools = [{"name": name} for name in [*FIXTURE["policies"], "refund_execute_unpublished"]]
    resource_templates = [{"uriTemplate": TEMPLATE, "name": "report"}]

    def __init__(self):
        self.calls = []

    async def call(self, name, args, access, *, timeout):
        self.calls.append((name, access, timeout))
        return {
            "isError": False,
            "structuredContent": {"subject": access},
            "content": [{"type": "resource_link", "uri": URI}],
        }

    async def read_resource(self, uri, access, *, timeout):
        return {"contents": [{"uri": uri, "mimeType": "text/plain", "text": access}]}


def access(role, subject="alice"):
    return ToolkitAccess(subject, FIXTURE["scope_sets"][role])


def setup():
    adapter = Adapter()
    tool_scopes = {k: list(v) for k, v in FIXTURE["policies"].items()}
    resource_scopes = {TEMPLATE: list(FIXTURE["resource_scopes"])}
    return (
        adapter,
        tool_scopes,
        resource_scopes,
        ApplicationToolkit([ToolkitMount(adapter, tool_scopes, resource_scopes)]),
    )


async def test_shared_scope_matrix_controls_discovery_and_dispatch():
    adapter, _, _, toolkit = setup()
    for role, expected in FIXTURE["authorized_tools"].items():
        assert [tool["name"] for tool in toolkit.list_tools(access(role))] == expected
    for name in ("recover_start", "refund_prepare", "refund_execute_unpublished"):
        with pytest.raises(ApplicationToolkitError, match="tool_not_found"):
            await toolkit.call(name, {}, access("support"), timeout=0.5)
    assert not adapter.calls
    await asyncio.gather(
        toolkit.call("recover_start", {}, access("operator"), timeout=0.5),
        toolkit.call("query_records", {}, access("support", "bob"), timeout=0.5),
    )
    assert adapter.calls == [("recover_start", "alice", 0.5), ("query_records", "bob", 0.5)]


async def test_link_and_resource_scopes_and_uris_share_a_policy():
    _, _, _, toolkit = setup()
    assert toolkit.list_resource_templates(access("support")) == []
    assert toolkit.list_resource_templates(access("operator"))[0]["uriTemplate"] == TEMPLATE
    assert not (await toolkit.call("query_records", {}, access("support"), timeout=0.5))["content"]
    assert (await toolkit.call("query_records", {}, access("operator"), timeout=0.5))["content"][0]["uri"] == URI
    with pytest.raises(ApplicationToolkitError, match="resource_not_found"):
        await toolkit.read_resource(URI, access("support"), timeout=0.5)
    for uri in (URI + "?x=1", URI + "/other", URI.replace("owned", "%6fwned"), URI.replace("report", "other")):
        with pytest.raises(ApplicationToolkitError, match="resource_not_found"):
            await toolkit.read_resource(uri, access("operator"), timeout=0.5)
    assert (await toolkit.read_resource(URI, access("operator", "bob"), timeout=0.5))["contents"][0]["text"] == "bob"


def test_registration_snapshots_rules_and_rejects_unknown_or_conflicting_selections():
    adapter, tool_scopes, resource_scopes, toolkit = setup()
    tool_scopes["query_records"].append("changed")
    resource_scopes[TEMPLATE].append("changed")
    assert toolkit.tool_scopes["query_records"] == ["records:read"]
    assert toolkit.resource_scopes[TEMPLATE] == ["reports:read"]
    for mounts in (
        [ToolkitMount(adapter, {"missing": ["records:read"]})],
        [ToolkitMount(adapter, {"query_records": []})],
        [ToolkitMount(adapter, resource_scopes={TEMPLATE: []})],
        [ToolkitMount(adapter, tool_scopes), ToolkitMount(adapter, tool_scopes)],
        [
            ToolkitMount(adapter, resource_scopes=resource_scopes),
            ToolkitMount(adapter, resource_scopes=resource_scopes),
        ],
    ):
        with pytest.raises(ValueError):
            ApplicationToolkit(mounts)
