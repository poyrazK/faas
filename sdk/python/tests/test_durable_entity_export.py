"""Owner state recovery wire contract; qualification is delegated."""

from faas_sdk.api.invocations import export_durable_entity, restore_durable_entity
from faas_sdk.models.durable_entity_restore_request import DurableEntityRestoreRequest
from faas_sdk.models.durable_entity_state_export import DurableEntityStateExport


def test_application_validation_preserves_deployment_pin():
    from uuid import UUID

    from faas_sdk.api.invocations import validate_durable_entity_restore
    from faas_sdk.models.durable_entity_restore_validation_response import DurableEntityRestoreValidationResponse

    deployment = UUID("11111111-1111-4111-8111-111111111111")
    exported = DurableEntityStateExport.from_dict(
        {
            "format": 1,
            "entity": {"account_id": "a", "app_id": "b", "environment_id": "e", "namespace": "ns", "key": "doc"},
            "version": 1,
            "data": {},
            "checksum": "a" * 64,
        }
    )
    request = DurableEntityRestoreRequest(
        namespace="ns",
        key="doc",
        request_id="stable",
        expected_version=2**64 - 1,
        export=exported,
        validation_deployment_id=deployment,
    )
    wire = validate_durable_entity_restore._get_kwargs("app", body=request)
    assert wire["url"] == "/v1/apps/app/entities/restore/validate"
    assert wire["json"]["validation_deployment_id"] == str(deployment)
    verdict = DurableEntityRestoreValidationResponse.from_dict(
        {"valid": True, "deployment_id": str(deployment), "expected_version": 2**64 - 1, "source_version": 1}
    )
    assert verdict.deployment_id == deployment
    assert verdict.expected_version == 2**64 - 1


def test_backup_and_preview_wire_contract():
    from faas_sdk.api.invocations import get_durable_entity_backup, list_durable_entity_backups
    from faas_sdk.models.durable_entity_restore_preview import DurableEntityRestorePreview

    listed = list_durable_entity_backups._get_kwargs("app", namespace="ns", key="doc/?雪", cursor="opaque+/=")
    assert listed["method"] == "get"
    assert listed["params"]["cursor"] == "opaque+/="
    read = get_durable_entity_backup._get_kwargs("app", namespace="ns", key="doc/?雪", backup_id="20261009T120000Z")
    assert read["params"]["backup_id"] == "20261009T120000Z"
    preview = DurableEntityRestorePreview.from_dict(
        {
            "current_version": 2**64 - 1,
            "source_version": 1,
            "expected_version_matches": False,
            "schema_relation": "unknown",
            "compatibility": "unverified",
            "alarm_pending": True,
            "outbox_pending": 2,
            "alarm_exhausted": False,
            "outbox_exhausted": True,
        }
    )
    assert preview.to_dict()["current_version"] == 2**64 - 1
    assert preview.to_dict()["compatibility"] == "unverified"


def test_export_restore_preserves_scope_and_full_integer_versions():
    envelope = {
        "format": 1,
        "entity": {
            "account_id": "account",
            "app_id": "app",
            "environment_id": "env",
            "namespace": "documents",
            "key": "document:123",
        },
        "version": 2**64 - 2,
        "data": {"count": 2**63},
        "checksum": "a" * 64,
    }
    exported = DurableEntityStateExport.from_dict(envelope)
    assert exported.to_dict() == envelope
    request = DurableEntityRestoreRequest(
        namespace="documents",
        key="document:123",
        request_id="stable",
        expected_version=2**64 - 1,
        export=exported,
    )
    kwargs = restore_durable_entity._get_kwargs("app/slug", body=request)
    assert kwargs["url"] == "/v1/apps/app%2Fslug/entities/restore"
    assert kwargs["json"]["export"] == envelope
    assert kwargs["json"]["expected_version"] == 2**64 - 1
    assert kwargs["json"]["request_id"] == "stable"
    read = export_durable_entity._get_kwargs("app/slug", namespace="documents", key="doc/?雪")
    assert read["method"] == "get"
    assert read["params"] == {"namespace": "documents", "key": "doc/?雪"}
