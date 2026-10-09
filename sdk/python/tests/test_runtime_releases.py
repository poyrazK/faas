from datetime import datetime

import httpx

from faas_sdk import FaaSClient
from faas_sdk.api.deployments import get_deployment_runtime, preview_runtime_upgrade
from faas_sdk.models import DeploymentRuntimeResponse, RuntimeUpgradePreviewResponse


def test_runtime_reads_preserve_unknown_identity_and_preview_without_mutation():
    target = "candidate&literal=value"
    current = {
        "deployment_id": "deployment",
        "status": "unknown",
        "reason": "No recorded binding.",
        "current": None,
        "releases": [],
    }
    preview = {
        "deployment_id": "deployment",
        "current": None,
        "target": {
            "id": "candidate",
            "runtime": "node22",
            "architecture": "amd64",
            "source_digest": "sha256:source",
            "guest_init_digest": "sha256:guest",
            "base_digest": "sha256:base",
            "layout_version": "v3",
            "published_at": "2026-10-05T12:00:00Z",
            "qualification": "not_evaluated",
        },
        "disposition": "blocked",
        "changes": [],
        "blockers": ["Unknown current identity."],
        "required_steps": [],
        "rebuild_required": False,
        "cold_start_required": False,
        "execution_available": False,
    }
    assert DeploymentRuntimeResponse.from_dict(current).to_dict() == current

    def respond(req: httpx.Request) -> httpx.Response:
        assert req.method == "GET"
        assert req.headers["Authorization"] == "Bearer token"
        if req.url.path.endswith("upgrade-preview"):
            assert req.url.params["target"] == target
            return httpx.Response(200, json=preview)
        assert req.url.path == "/v1/deployments/deployment/runtime"
        return httpx.Response(200, json=current)

    client = FaaSClient(
        base_url="https://api.example.test", token="token", httpx_args={"transport": httpx.MockTransport(respond)}
    )
    try:
        got = get_deployment_runtime.sync("deployment", client=client.inner)
        assert isinstance(got, DeploymentRuntimeResponse)
        assert got.current is None
        result = preview_runtime_upgrade.sync("deployment", client=client.inner, target=target)
        assert isinstance(result, RuntimeUpgradePreviewResponse)
        assert result.current is None
        assert result.execution_available is False
        assert result.disposition == "blocked"
        encoded = result.to_dict()
        assert datetime.fromisoformat(encoded["target"].pop("published_at")) == datetime.fromisoformat(
            preview["target"]["published_at"]
        )
        expected = {**preview, "target": {k: v for k, v in preview["target"].items() if k != "published_at"}}
        assert encoded == expected
    finally:
        client.httpx_client.close()
