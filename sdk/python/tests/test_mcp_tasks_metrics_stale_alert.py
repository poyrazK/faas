from faas_sdk.preview.models.alert_rule_response_metric import check_alert_rule_response_metric
from faas_sdk.preview.models.create_alert_rule_request import CreateAlertRuleRequest
from faas_sdk.preview.models.create_alert_rule_request_metric import check_create_alert_rule_request_metric
from faas_sdk.preview.models.update_alert_rule_request_metric import check_update_alert_rule_request_metric


def test_mcp_tasks_metrics_stale_metric_is_available_in_alert_rule_sdk():
    metric = "mcp_tasks_metrics_stale"
    assert check_create_alert_rule_request_metric(metric) == metric
    assert check_alert_rule_response_metric(metric) == metric
    assert check_update_alert_rule_request_metric(metric) == metric

    request = CreateAlertRuleRequest(
        name="mcp-task-metrics-stale",
        metric=metric,
        comparison="gt",
        threshold=0,
        window_spec="5m",
        webhook_url="https://example.com/hook",
        webhook_secret="fixture",
    )
    assert request.to_dict()["metric"] == metric
