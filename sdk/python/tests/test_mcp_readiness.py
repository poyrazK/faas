from faas_sdk.models.mcp_readiness_snapshot import MCPReadinessSnapshot


def test_durable_readiness_round_trip():
    revision = "7e53717e-9b82-4ea4-a594-4a1d8aa2a8bb"
    deployment = "cb136aee-df09-4663-9f09-810fd6e4d6a2"
    timestamp = "2026-10-15T12:00:00Z"
    payload = {
        "policy": {
            "revision": revision,
            "enabled": True,
            "endpoint": "/mcp",
            "max_age_seconds": 300,
            "roles": [
                {"name": "reader", "protocol_version": "2026-07-28", "catalog_format": 2, "catalog_sha256": "a" * 64}
            ],
            "updated_at": timestamp,
        },
        "evidence": [
            {
                "id": "077880de-62ae-40b8-8efc-5d390662604c",
                "deployment_id": deployment,
                "role": "reader",
                "policy_revision": revision,
                "artifact_sha256": "b" * 64,
                "protocol_version": "2026-07-28",
                "catalog_format": 2,
                "catalog_sha256": "a" * 64,
                "passed": True,
                "observed_at": timestamp,
                "checks": [{"name": "ping", "status": "passed"}],
            }
        ],
        "overrides": [],
    }
    payload["policy"]["roles"][0]["monitor"] = {
        "enabled": True,
        "interval_seconds": 60,
        "failure_threshold": 3,
        "secret_ref": "secret:MCP_READINESS_TOKEN",
    }
    payload["monitoring"] = [
        {
            "deployment_id": deployment,
            "role": "reader",
            "policy_revision": revision,
            "next_probe_at": timestamp,
            "consecutive_failures": 3,
            "failure_threshold": 3,
            "state": "firing",
            "reason": "catalog_drift",
            "history": [{"observed_at": timestamp, "passed": False, "reason": "catalog_drift"}],
        }
    ]
    result = MCPReadinessSnapshot.from_dict(payload).to_dict()
    assert result["policy"]["roles"][0]["monitor"]["secret_ref"] == "secret:MCP_READINESS_TOKEN"
    assert result["monitoring"][0]["history"][0]["reason"] == "catalog_drift"
    assert result["monitoring"][0]["state"] == "firing"
    assert result["policy"]["roles"][0]["catalog_format"] == 2
    assert result["evidence"][0]["deployment_id"] == deployment
    assert result["evidence"][0]["policy_revision"] == revision
    assert result["evidence"][0]["checks"] == [{"name": "ping", "status": "passed"}]
    assert result["overrides"] == []


def test_managed_oauth_readiness_round_trip():
    from faas_sdk.models.mcp_readiness_monitor_config import MCPReadinessMonitorConfig
    from faas_sdk.models.mcp_readiness_monitor_status import MCPReadinessMonitorStatus

    connection = {"role": "reader", "revision": "7e53717e-9b82-4ea4-a594-4a1d8aa2a8bb"}
    config = {
        "enabled": True,
        "interval_seconds": 60,
        "failure_threshold": 3,
        "oauth_connection": connection,
    }
    assert MCPReadinessMonitorConfig.from_dict(config).to_dict() == config
    payload = {
        "deployment_id": "cb136aee-df09-4663-9f09-810fd6e4d6a2",
        "role": "reader",
        "policy_revision": "7e53717e-9b82-4ea4-a594-4a1d8aa2a8bb",
        "next_probe_at": "2026-10-15T12:00:00Z",
        "consecutive_failures": 3,
        "failure_threshold": 3,
        "state": "firing",
        "history": [],
        "oauth_connection": connection,
        "recovery_action": "reconnect",
    }
    result = MCPReadinessMonitorStatus.from_dict(payload).to_dict()
    assert result["oauth_connection"] == connection
    assert result["recovery_action"] == "reconnect"


def test_environment_oauth_binding_round_trip():
    from faas_sdk.models.mcp_readiness_monitor_config import MCPReadinessMonitorConfig

    payload = {
        "enabled": True,
        "interval_seconds": 60,
        "failure_threshold": 2,
        "oauth_connections": {
            "staging": {
                "role": "staging-reader",
                "revision": "7e53717e-9b82-4ea4-a594-4a1d8aa2a8bb",
                "scope": "staging",
            }
        },
    }
    result = MCPReadinessMonitorConfig.from_dict(payload).to_dict()
    assert result["oauth_connections"] == payload["oauth_connections"]
    assert "oauth_connection" not in result
    assert "secret_ref" not in result


def test_environment_readiness_policy_round_trip():
    from faas_sdk.models.mcp_readiness_policy import MCPReadinessPolicy
    from faas_sdk.models.set_mcp_readiness_policy_request import SetMCPReadinessPolicyRequest

    payload = {
        "scope": "staging",
        "enabled": False,
        "endpoint": "/staging-mcp",
        "max_age_seconds": 300,
        "roles": [],
        "reason": "reviewed staging policy",
    }
    request = SetMCPReadinessPolicyRequest.from_dict({**payload, "expected_revision": ""})
    assert request.scope == "staging"
    assert request.to_dict()["scope"] == "staging"
    policy = MCPReadinessPolicy.from_dict(
        {**payload, "revision": "7e53717e-9b82-4ea4-a594-4a1d8aa2a8bb", "updated_at": "2026-10-15T12:00:00Z"}
    )
    assert policy.scope == "staging"
    assert policy.to_dict()["scope"] == "staging"
