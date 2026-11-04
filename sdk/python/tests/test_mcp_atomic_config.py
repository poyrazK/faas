"""ADR-20261103171917: durable request identities cross the SDK wire intact."""

import importlib
from uuid import UUID

import pytest


@pytest.mark.parametrize("prefix", ["faas_sdk", "faas_sdk.preview"])
def test_atomic_variable_batch_serializes_the_original_request(prefix):
    models = importlib.import_module(f"{prefix}.models")
    apply = importlib.import_module(f"{prefix}.api.env.apply_env_batch")
    request_id = UUID("582a9075-03d9-4434-a0ef-e4475508d928")
    request = models.EnvBatchRequest(
        request_id=request_id,
        expected_revision=7,
        values=models.EnvBatchRequestValues.from_dict({"LOG_LEVEL": "debug"}),
        delete=["OLD_MODE"],
    )
    original = {
        "request_id": str(request_id),
        "expected_revision": 7,
        "values": {"LOG_LEVEL": "debug"},
        "delete": ["OLD_MODE"],
    }
    assert apply._get_kwargs("alpha", body=request)["json"] == original
    assert models.EnvBatchRequest.from_dict(original).to_dict() == original


@pytest.mark.parametrize("prefix", ["faas_sdk", "faas_sdk.preview"])
def test_database_reservation_is_explicit_and_stable(prefix):
    models = importlib.import_module(f"{prefix}.models")
    request_id = UUID("582a9075-03d9-4434-a0ef-e4475508d928")
    legacy = models.CreateManagedPostgresDatabaseRequest(name="agent-db", region="eu")
    assert "request_id" not in legacy.to_dict()
    reserved = models.CreateManagedPostgresDatabaseRequest(name="agent-db", region="eu", request_id=request_id)
    assert reserved.to_dict()["request_id"] == str(request_id)
    assert models.CreateManagedPostgresDatabaseRequest.from_dict(reserved.to_dict()).request_id == request_id
