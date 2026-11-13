from importlib import import_module

import pytest


@pytest.mark.parametrize("package", ["faas_sdk", "faas_sdk.preview"])
@pytest.mark.parametrize("kind", ["resize", "compute_policy_change"])
def test_confirmed_rejection_decodes_in_public_and_preview_clients(package: str, kind: str) -> None:
    module = import_module(f"{package}.models.managed_postgres_{kind}")
    model = getattr(module, "ManagedPostgresResize" if kind == "resize" else "ManagedPostgresComputePolicyChange")
    payload = {
        "id": "33333333-3333-4333-8333-333333333333",
        "database_id": "orders",
        "generation": 2,
        "state": "failed",
        "last_error_code": "unsupported",
        "connection_interruption_expected": True,
        "created_at": "2026-11-04T22:00:00Z",
        "completed_at": "2026-11-04T22:01:00Z",
    }
    if kind == "resize":
        payload.update(from_class="development", target_class="burstable")
    else:
        payload.update(from_scale_to_zero=True, target_scale_to_zero=False)
    receipt = model.from_dict(payload)
    assert receipt.state == "failed"
    assert receipt.last_error_code == "unsupported"
    assert receipt.to_dict()["state"] == "failed"
    assert receipt.completed_at.isoformat() == "2026-11-04T22:01:00+00:00"
