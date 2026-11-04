from faas_sdk.preview.models.scenario_test_chaos_rule import ScenarioTestChaosRule


def test_scenario_retry_header_round_trip():
    for status in (429, 503):
        wire = {
            "to": "inventory",
            "kind": "http_status",
            "percent": 100,
            "seed": 0,
            "status_code": status,
            "retry_after_seconds": 1,
        }
        rule = ScenarioTestChaosRule.from_dict(wire)
        assert rule.status_code == status
        assert rule.retry_after_seconds == 1
        assert rule.to_dict() == wire


def test_scenario_body_fault_round_trip():
    for kind in ("http_malformed_json", "http_truncated_body", "http_response_lost"):
        wire = {"to": "inventory", "kind": kind, "percent": 100, "seed": 0}
        rule = ScenarioTestChaosRule.from_dict(wire)
        assert rule.kind == kind
        assert rule.to_dict() == wire


def test_scenario_rule_round_trips_request_correlation_scope():
    from faas_sdk.preview.models.scenario_test_chaos_rule import ScenarioTestChaosRule

    source = {
        "from": "caller",
        "to": "payment",
        "kind": "http_status",
        "status_code": 503,
        "percent": 100,
        "seed": 1,
        "correlation_scope": "1234567890abcdef",
    }
    assert ScenarioTestChaosRule.from_dict(source).to_dict() == source
