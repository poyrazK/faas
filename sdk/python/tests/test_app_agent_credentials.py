"""ADR-20261103155008: new agent credentials preserve legacy request bodies."""

import importlib

import pytest


@pytest.mark.parametrize("prefix", ["faas_sdk", "faas_sdk.preview"])
def test_standard_deploy_token_request_remains_compatible(prefix):
    models = importlib.import_module(f"{prefix}.models")
    create = importlib.import_module(f"{prefix}.api.apps.create_deploy_token")
    # N-1 apid rejects unknown fields: an SDK default must not implicitly send
    # agent=false when the caller is minting an ordinary deploy token.
    request = models.CreateDeployTokenRequest(label="ci")
    assert create._get_kwargs("my-api", body=request)["json"] == {"label": "ci"}


@pytest.mark.parametrize("prefix", ["faas_sdk", "faas_sdk.preview"])
def test_explicit_app_agent_scopes_survive_serialization(prefix):
    models = importlib.import_module(f"{prefix}.models")
    request = models.CreateDeployTokenRequest(agent=True, scopes=["deploy:write", "env:write"])
    assert models.CreateDeployTokenRequest.from_dict(request.to_dict()).to_dict() == {
        "agent": True,
        "scopes": ["deploy:write", "env:write"],
    }
