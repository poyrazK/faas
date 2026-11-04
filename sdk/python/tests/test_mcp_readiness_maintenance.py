from faas_sdk.preview.models.create_mcp_readiness_maintenance_request import CreateMCPReadinessMaintenanceRequest
from faas_sdk.preview.models.mcp_readiness_maintenance_page import MCPReadinessMaintenancePage


def test_maintenance_window_round_trip():
    window = dict(
        id="00000000-0000-0000-0000-000000000001",
        scope="staging",
        starts_at="2026-10-16T00:00:00Z",
        ends_at="2026-10-16T01:00:00Z",
        created_at="2026-10-16T00:00:00Z",
        reason="upgrade",
        created_by="00000000-0000-0000-0000-000000000002",
        cancelled_at="2026-10-16T00:30:00Z",
        cancelled_by="00000000-0000-0000-0000-000000000002",
        cancel_reason="complete",
    )
    loaded = MCPReadinessMaintenancePage.from_dict(dict(windows=[window])).to_dict()["windows"][0]
    assert loaded["cancel_reason"] == "complete"
    assert loaded["scope"] == "staging"
    request = CreateMCPReadinessMaintenanceRequest.from_dict(dict(ends_at=window["ends_at"], reason="silence"))
    assert "starts_at" not in request.to_dict()
