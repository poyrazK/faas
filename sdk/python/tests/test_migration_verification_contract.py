from uuid import UUID

from faas_sdk.api.apps import (
    advance_migration_verification,
    cancel_migration_verification,
    create_migration_verification,
)
from faas_sdk.models.create_migration_verification_request import CreateMigrationVerificationRequest
from faas_sdk.models.migration_dependency_checks import MigrationDependencyChecks
from faas_sdk.models.migration_service_connection_check import MigrationServiceConnectionCheck


def test_verification_uses_names_and_preserves_explicit_cold_wake_consent():
    run = UUID("33333333-3333-4333-8333-333333333333")
    body = CreateMigrationVerificationRequest(
        id=run,
        deployment_id=UUID("11111111-1111-4111-8111-111111111111"),
        checks=MigrationDependencyChecks(
            postgres_env_key="DATABASE_URL",
            service_connections=[
                MigrationServiceConnectionCheck(
                    env_key="API_URL", service="api", deployment_id=UUID("22222222-2222-4222-8222-222222222222")
                )
            ],
        ),
        smoke_path="/ready",
        cold_wake=True,
    )
    request = create_migration_verification._get_kwargs(slug="shop-api", body=body)
    assert request["method"] == "post"
    assert request["url"] == "/v1/apps/shop-api/migration-verifications"
    assert request["json"]["checks"] == {
        "postgres_env_key": "DATABASE_URL",
        "service_connections": [
            {"env_key": "API_URL", "service": "api", "deployment_id": "22222222-2222-4222-8222-222222222222"}
        ],
    }
    assert request["json"]["cold_wake"] is True
    assert request["json"]["id"] == str(run)
    advance = advance_migration_verification._get_kwargs(slug="shop-api", run=run)
    assert advance["url"].endswith(f"/{run}/advance")


def test_cancellation_preserves_the_original_receipt():
    run = UUID("33333333-3333-4333-8333-333333333333")
    request = cancel_migration_verification._get_kwargs(slug="shop-api", run=run)
    assert request["method"] == "post"
    assert request["url"] == f"/v1/apps/shop-api/migration-verifications/{run}/cancel"
