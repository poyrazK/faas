from uuid import UUID

from faas_sdk.preview.api.apps import (
    act_on_migration_cutover,
    create_migration_cutover,
    get_migration_cutover,
    list_migration_cutovers,
)
from faas_sdk.preview.models.create_migration_cutover_request import CreateMigrationCutoverRequest
from faas_sdk.preview.models.migration_cutover_action_request import MigrationCutoverActionRequest
from faas_sdk.preview.models.migration_dns_record import MigrationDNSRecord


def test_cutover_preserves_original_dns_and_stable_plan_identity():
    plan = UUID("44444444-4444-4444-8444-444444444444")
    body = CreateMigrationCutoverRequest(
        id=plan,
        verification_id=UUID("33333333-3333-4333-8333-333333333333"),
        hostname="api.example.test",
        previous_records=[MigrationDNSRecord(type_="CNAME", value="old.provider.test", ttl=300)],
        previous_proxy_enabled=True,
        max_5xx_rate_bps=100,
        max_p95_ms=1000,
    )
    request = create_migration_cutover._get_kwargs(slug="shop-api", body=body)
    assert request["method"] == "post"
    assert request["url"] == "/v1/apps/shop-api/migration-cutovers"
    assert request["json"]["id"] == str(plan)
    assert request["json"]["previous_records"] == [{"type": "CNAME", "value": "old.provider.test", "ttl": 300}]
    assert request["json"]["previous_proxy_enabled"] is True
    assert get_migration_cutover._get_kwargs(slug="shop-api", plan=plan)["url"].endswith(str(plan))
    assert list_migration_cutovers._get_kwargs(slug="shop-api")["method"] == "get"


def test_cutover_completion_carries_explicit_coverage_acknowledgement():
    plan = UUID("44444444-4444-4444-8444-444444444444")
    request = act_on_migration_cutover._get_kwargs(
        slug="shop-api",
        plan=plan,
        body=MigrationCutoverActionRequest(action="complete", acknowledge_limited_traffic=True),
    )
    assert request["method"] == "post"
    assert request["url"] == f"/v1/apps/shop-api/migration-cutovers/{plan}/actions"
    assert request["json"] == {"action": "complete", "acknowledge_limited_traffic": True}
