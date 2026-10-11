import datetime
from uuid import UUID

import httpx

from faas_sdk.api.alert_rules import get_alert_rollback, list_alert_rollbacks
from faas_sdk.client import Client
from faas_sdk.models import AlertRollback


def test_alert_rollback_gets_preserve_exact_pair_and_blocked_state():
    fire = UUID("3e9f323a-ade6-442b-8444-c91da107fe44")
    receipt = AlertRollback(
        id=fire,
        rule_id=UUID("a2b9cc53-907f-4b5c-88a4-fd0c21214556"),
        account_id=UUID("b3b9cc53-907f-4b5c-88a4-fd0c21214556"),
        app_id=UUID("142b7504-f03a-4ee2-aeb3-14d922a845d4"),
        scope="default",
        candidate_deployment_id=UUID("5b87c415-7c93-4932-acab-a3c90e98be86"),
        predecessor_deployment_id=UUID("6b87c415-7c93-4932-acab-a3c90e98be86"),
        status="blocked",
        reason="alert fired",
        observed_value=42,
        fired_at=datetime.datetime.now(datetime.UTC),
        updated_at=datetime.datetime.now(datetime.UTC),
        service=True,
        service_request_id=fire,
        service_phase="pending",
    )
    client = Client(base_url="https://example.test")
    kwargs = get_alert_rollback._get_kwargs("api", fire)
    assert kwargs["method"] == "get"
    assert kwargs["url"] == f"/v1/apps/api/alert-rollbacks/{fire}"
    read = get_alert_rollback._parse_response(client=client, response=httpx.Response(200, json=receipt.to_dict()))
    assert isinstance(read, AlertRollback)
    assert read.candidate_deployment_id == receipt.candidate_deployment_id
    assert read.predecessor_deployment_id == receipt.predecessor_deployment_id
    assert read.status == "blocked"
    assert read.service is True
    assert read.service_request_id == fire
    assert read.service_phase == "pending"
    assert list_alert_rollbacks._get_kwargs("api")["method"] == "get"
    rows = list_alert_rollbacks._parse_response(client=client, response=httpx.Response(200, json=[receipt.to_dict()]))
    assert len(rows) == 1
    assert rows[0].id == fire


def test_historical_alert_receipt_and_window_wire():
    from faas_sdk.models import CreateAlertRuleRequest, UpdateAlertRuleRequest

    fire = UUID("3e9f323a-ade6-442b-8444-c91da107fe44")
    wire = {
        "id": str(fire),
        "rule_id": str(fire),
        "account_id": str(fire),
        "app_id": str(fire),
        "scope": "default",
        "status": "pending",
        "reason": "alert",
        "observed_value": 42,
        "fired_at": "2026-10-05T00:00:00Z",
        "updated_at": "2026-10-05T00:00:00Z",
        "historical": True,
        "rollback_operation_id": str(fire),
        "rollback_phase": "preparing",
    }
    wire["deployment_evidence"] = {
        "version": 1,
        "deployment_id": str(fire),
        "metric": "error_rate_pct",
        "comparison": "gt",
        "threshold": 1,
        "window_spec": "5m",
        "cutover_at": "2026-10-05T00:00:00+00:00",
        "window_start": "2026-10-05T00:01:00+00:00",
        "window_end": "2026-10-05T00:04:00+00:00",
        "requests": 20,
        "server_errors": 2,
        "minimum_requests": 20,
        "error_rate_pct": 10,
        "status": "breached",
    }
    read = AlertRollback.from_dict(wire)
    assert read.deployment_evidence.requests == 20
    assert read.deployment_evidence.server_errors == 2
    assert read.to_dict()["deployment_evidence"] == wire["deployment_evidence"]
    assert read.historical is True
    assert read.rollback_operation_id == fire
    assert read.rollback_phase == "preparing"
    assert read.to_dict()["rollback_operation_id"] == str(fire)
    create = CreateAlertRuleRequest(
        name="recovery",
        metric="error_rate_pct",
        comparison="gt",
        threshold=1,
        window_spec="5m",
        webhook_url="https://example.com/hook",
        webhook_secret="fixture",
        action="rollback",
        post_deploy_rollback_window_seconds=600,
    )
    assert create.to_dict()["post_deploy_rollback_window_seconds"] == 600
    assert "post_deploy_rollback_window_seconds" not in UpdateAlertRuleRequest(name="renamed").to_dict()
    assert (
        UpdateAlertRuleRequest(post_deploy_rollback_window_seconds=0).to_dict()["post_deploy_rollback_window_seconds"]
        == 0
    )
