from faas_sdk.api.deployments import promote_deployment_with_application_ack
from faas_sdk.models.app_manifest import AppManifest
from faas_sdk.models.binding_application_adoption import BindingApplicationAdoption
from faas_sdk.models.binding_promotion_request import BindingPromotionRequest


def test_strict_route_and_independent_adoption_versions() -> None:
    target = "01234567-89ab-cdef-0123-456789abcdef"
    kwargs = promote_deployment_with_application_ack._get_kwargs(
        target, body=BindingPromotionRequest(require_application_ack=True)
    )
    assert kwargs["method"] == "post"
    assert kwargs["url"] == f"/v1/deployments/{target}/promote-with-application-ack"
    assert kwargs["json"]["require_application_ack"] is True
    receipt = {
        "source": "application_ack",
        "status": "current",
        "observed_at": "2026-10-03T00:00:00+00:00",
        "complete": True,
        "secrets_expected": 1,
        "secrets_observed": 1,
        "reload": {"current": 0, "failed": 0, "stale": 1, "unknown": 0},
        "application": {"current": 1, "failed": 0, "stale": 0, "unknown": 0},
        "targets": [
            {
                "deployment_id": target,
                "instance_id": target,
                "workload_name": "worker",
                "runtime_state": "running",
                "key": "DATABASE_URL",
                "reload_support": "enabled",
                "current_version": 2,
                "reload_version": 1,
                "application_ack_version": 2,
                "application_ack": "applied",
                "process_generation": "a" * 32,
                "application_ack_generation": "a" * 32,
            }
        ],
    }
    parsed = BindingApplicationAdoption.from_dict(receipt)
    assert parsed.application.current == 1
    assert parsed.reload.stale == 1
    assert parsed.targets[0].application_ack_version == 2
    assert parsed.targets[0].reload_version == 1
    assert parsed.targets[0].process_generation == "a" * 32
    assert parsed.targets[0].application_ack_generation == "a" * 32
    assert parsed.to_dict() == receipt


def test_manifest_preserves_typed_secret_reload_readiness() -> None:
    body = {"entrypoint": ["node", "server.js"], "secret_reload_signal": "SIGHUP", "secret_reload_readiness": True}
    manifest = AppManifest.from_dict(body)
    assert manifest.secret_reload_readiness is True
    assert manifest.to_dict() == body
