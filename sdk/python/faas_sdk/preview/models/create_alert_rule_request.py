from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.create_alert_rule_request_action import (
    CreateAlertRuleRequestAction,
    check_create_alert_rule_request_action,
)
from ..models.create_alert_rule_request_comparison import (
    CreateAlertRuleRequestComparison,
    check_create_alert_rule_request_comparison,
)
from ..models.create_alert_rule_request_failure_source import (
    CreateAlertRuleRequestFailureSource,
    check_create_alert_rule_request_failure_source,
)
from ..models.create_alert_rule_request_metric import (
    CreateAlertRuleRequestMetric,
    check_create_alert_rule_request_metric,
)
from ..models.create_alert_rule_request_window_spec import (
    CreateAlertRuleRequestWindowSpec,
    check_create_alert_rule_request_window_spec,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="CreateAlertRuleRequest")


@_attrs_define
class CreateAlertRuleRequest:
    """Create an alert rule on an app."""

    name: str
    metric: CreateAlertRuleRequestMetric
    """Selected metric for the existing alert rule. MCP task age reads a fresh app gauge in seconds and degrades
    when unavailable; mcp_tasks_metrics_stale is 1 when either task gauge is missing, stale, or invalid. Min=0
    worker apps also require a fresh observer heartbeat with value 1. mcp_task_promotion_hold_age_seconds reads the
    oldest active hold directly from state (seconds, zero with no holds); its alerts support webhook action only.
   """
    comparison: CreateAlertRuleRequestComparison
    """above_baseline and below_baseline (ADR-744, preview behind FAAS_ANOMALY_ALERTS_ENABLED) compare against the
    median of the same metric at the same time of day over the previous 7 days; threshold is then a multiplier
    (1.5-20 above, 0.05-0.67 below). Supported for error_rate_pct, latency_p95_ms, latency_p99_ms, cold_start_pct
    and request_count (below_baseline: request_count only)."""
    threshold: float
    window_spec: CreateAlertRuleRequestWindowSpec
    mcp_readiness_scope: str | Unset = UNSET
    """Readiness environment requested when creating this alert. Immutable readiness environment selector. Omitted
    or empty means every environment; default selects only default. Valid only for mcp_readiness_unhealthy."""
    mcp_min_calls: int | Unset = UNSET
    """Observation minimum requested for the new protocol alert. MCP protocol alerts only. Minimum observations in
    every rolling window; defaults to 20 on create. Latency uses measured executions. Omit for other metrics."""
    mcp_for_minutes: int | Unset = UNSET
    """Sustained duration requested for the new protocol alert. MCP protocol alerts only. Continuous breach
    duration sampled every 30 seconds; defaults to 10 on create. Missing observations interrupt eligibility. Webhook
    action, app scope, gt/gte and windows 5m/15m/1h are required. Error thresholds are percent (0..100), execution
    p95 thresholds are milliseconds (0..300000)."""
    event_subscription_id: UUID | Unset = UNSET
    """Event subscription id in the create alert rule request: immutable subscription selector. Required only for
    event consumer metrics; webhook action and windows up to 24h are required."""
    edge_rule_id: UUID | Unset = UNSET
    """ADR-1192. Edge rule to count for edge_rule_hits (enforced matches), edge_rule_log_hits (log-mode matches) or
    edge_rule_activity (matches in either mode, so the alert survives a switch from log mode to enforce); omit to
    count every rule of the app. Immutable. Edge-rule metrics are webhook-only and take window_spec 1h, 6h, 24h or
    7d (hourly hit buckets, kept 14 days)."""
    post_deploy_rollback_window_seconds: int | Unset = UNSET
    """Enable completed-release rollback for this many seconds after cutover; 0 disables it. Requires
    action=rollback. Only deployment-specific error_rate_pct breaches with gt or gte comparisons can qualify."""
    enabled: bool | Unset = UNSET
    failure_source: CreateAlertRuleRequestFailureSource | Unset = UNSET
    """Required when metric == failed_invocations; omit otherwise (xor_chk)."""
    custom_metric_name: str | Unset = UNSET
    """Required when metric is custom_metric; omit otherwise. A gauge is compared on its average over the window
    and a counter on its per-second rate. custom_metric rules support webhook action only and windows up to 24h."""
    log_query: str | Unset = UNSET
    """Required when metric is log_search_matches; omit otherwise. URL query string of log search filters (source,
    level, status, route, deployment_id, request_id, trace_id, q); the rule counts matching log events in its
    window. Webhook action only, windows up to 24h."""
    slo_id: UUID | Unset = UNSET
    """Required for slo_budget_burn (multi-window burn rate; 14.4 is the paging threshold) and
    slo_budget_remaining_pct (percent of the error budget left); names one of this app's SLOs. The rule window_spec
    is ignored for these metrics."""
    synthetic_check_id: UUID | Unset = UNSET
    """Required for synthetic_check_consecutive_failures (failed runs since the last success; window_spec ignored)
    and synthetic_check_latency_p95_ms (p95 of successful runs in the window); names one of this app's checks."""
    webhook_url: str | Unset = UNSET
    """Signed webhook destination. Required with webhook_secret unless channel_ids is set."""
    channel_ids: list[UUID] | Unset = UNSET
    """Notification channels (ADR-749) to deliver fires and resolves to. With at least one, webhook_url and
    webhook_secret may be omitted."""
    webhook_secret: str | Unset = UNSET
    """Plaintext HMAC secret (max 256 bytes). Sealed at rest; never echoed."""
    cooldown_minutes: int | Unset = UNSET
    action: CreateAlertRuleRequestAction | Unset = "webhook"
    """What to do when the rule fires. Omit to default to webhook. Pre-auth target and MCP task, event consumer,
    and workflow metrics support webhook only."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        metric: str = self.metric

        comparison: str = self.comparison

        threshold = self.threshold

        window_spec: str = self.window_spec

        mcp_readiness_scope = self.mcp_readiness_scope

        mcp_min_calls = self.mcp_min_calls

        mcp_for_minutes = self.mcp_for_minutes

        event_subscription_id: str | Unset = UNSET
        if not isinstance(self.event_subscription_id, Unset):
            event_subscription_id = str(self.event_subscription_id)

        edge_rule_id: str | Unset = UNSET
        if not isinstance(self.edge_rule_id, Unset):
            edge_rule_id = str(self.edge_rule_id)

        post_deploy_rollback_window_seconds = self.post_deploy_rollback_window_seconds

        enabled = self.enabled

        failure_source: str | Unset = UNSET
        if not isinstance(self.failure_source, Unset):
            failure_source = self.failure_source

        custom_metric_name = self.custom_metric_name

        log_query = self.log_query

        slo_id: str | Unset = UNSET
        if not isinstance(self.slo_id, Unset):
            slo_id = str(self.slo_id)

        synthetic_check_id: str | Unset = UNSET
        if not isinstance(self.synthetic_check_id, Unset):
            synthetic_check_id = str(self.synthetic_check_id)

        webhook_url = self.webhook_url

        channel_ids: list[str] | Unset = UNSET
        if not isinstance(self.channel_ids, Unset):
            channel_ids = []
            for channel_ids_item_data in self.channel_ids:
                channel_ids_item = str(channel_ids_item_data)
                channel_ids.append(channel_ids_item)

        webhook_secret = self.webhook_secret

        cooldown_minutes = self.cooldown_minutes

        action: str | Unset = UNSET
        if not isinstance(self.action, Unset):
            action = self.action

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "name": name,
                "metric": metric,
                "comparison": comparison,
                "threshold": threshold,
                "window_spec": window_spec,
            }
        )
        if mcp_readiness_scope is not UNSET:
            field_dict["mcp_readiness_scope"] = mcp_readiness_scope
        if mcp_min_calls is not UNSET:
            field_dict["mcp_min_calls"] = mcp_min_calls
        if mcp_for_minutes is not UNSET:
            field_dict["mcp_for_minutes"] = mcp_for_minutes
        if event_subscription_id is not UNSET:
            field_dict["event_subscription_id"] = event_subscription_id
        if edge_rule_id is not UNSET:
            field_dict["edge_rule_id"] = edge_rule_id
        if post_deploy_rollback_window_seconds is not UNSET:
            field_dict["post_deploy_rollback_window_seconds"] = post_deploy_rollback_window_seconds
        if enabled is not UNSET:
            field_dict["enabled"] = enabled
        if failure_source is not UNSET:
            field_dict["failure_source"] = failure_source
        if custom_metric_name is not UNSET:
            field_dict["custom_metric_name"] = custom_metric_name
        if log_query is not UNSET:
            field_dict["log_query"] = log_query
        if slo_id is not UNSET:
            field_dict["slo_id"] = slo_id
        if synthetic_check_id is not UNSET:
            field_dict["synthetic_check_id"] = synthetic_check_id
        if webhook_url is not UNSET:
            field_dict["webhook_url"] = webhook_url
        if channel_ids is not UNSET:
            field_dict["channel_ids"] = channel_ids
        if webhook_secret is not UNSET:
            field_dict["webhook_secret"] = webhook_secret
        if cooldown_minutes is not UNSET:
            field_dict["cooldown_minutes"] = cooldown_minutes
        if action is not UNSET:
            field_dict["action"] = action

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        name = d.pop("name")

        metric = check_create_alert_rule_request_metric(d.pop("metric"))

        comparison = check_create_alert_rule_request_comparison(d.pop("comparison"))

        threshold = d.pop("threshold")

        window_spec = check_create_alert_rule_request_window_spec(d.pop("window_spec"))

        mcp_readiness_scope = d.pop("mcp_readiness_scope", UNSET)

        mcp_min_calls = d.pop("mcp_min_calls", UNSET)

        mcp_for_minutes = d.pop("mcp_for_minutes", UNSET)

        _event_subscription_id = d.pop("event_subscription_id", UNSET)
        event_subscription_id: UUID | Unset
        if isinstance(_event_subscription_id, Unset):
            event_subscription_id = UNSET
        else:
            event_subscription_id = UUID(_event_subscription_id)

        _edge_rule_id = d.pop("edge_rule_id", UNSET)
        edge_rule_id: UUID | Unset
        if isinstance(_edge_rule_id, Unset):
            edge_rule_id = UNSET
        else:
            edge_rule_id = UUID(_edge_rule_id)

        post_deploy_rollback_window_seconds = d.pop("post_deploy_rollback_window_seconds", UNSET)

        enabled = d.pop("enabled", UNSET)

        _failure_source = d.pop("failure_source", UNSET)
        failure_source: CreateAlertRuleRequestFailureSource | Unset
        if isinstance(_failure_source, Unset):
            failure_source = UNSET
        else:
            failure_source = check_create_alert_rule_request_failure_source(_failure_source)

        custom_metric_name = d.pop("custom_metric_name", UNSET)

        log_query = d.pop("log_query", UNSET)

        _slo_id = d.pop("slo_id", UNSET)
        slo_id: UUID | Unset
        if isinstance(_slo_id, Unset):
            slo_id = UNSET
        else:
            slo_id = UUID(_slo_id)

        _synthetic_check_id = d.pop("synthetic_check_id", UNSET)
        synthetic_check_id: UUID | Unset
        if isinstance(_synthetic_check_id, Unset):
            synthetic_check_id = UNSET
        else:
            synthetic_check_id = UUID(_synthetic_check_id)

        webhook_url = d.pop("webhook_url", UNSET)

        _channel_ids = d.pop("channel_ids", UNSET)
        channel_ids: list[UUID] | Unset = UNSET
        if _channel_ids is not UNSET:
            channel_ids = []
            for channel_ids_item_data in _channel_ids:
                channel_ids_item = UUID(channel_ids_item_data)

                channel_ids.append(channel_ids_item)

        webhook_secret = d.pop("webhook_secret", UNSET)

        cooldown_minutes = d.pop("cooldown_minutes", UNSET)

        _action = d.pop("action", UNSET)
        action: CreateAlertRuleRequestAction | Unset
        if isinstance(_action, Unset):
            action = UNSET
        else:
            action = check_create_alert_rule_request_action(_action)

        create_alert_rule_request = cls(
            name=name,
            metric=metric,
            comparison=comparison,
            threshold=threshold,
            window_spec=window_spec,
            mcp_readiness_scope=mcp_readiness_scope,
            mcp_min_calls=mcp_min_calls,
            mcp_for_minutes=mcp_for_minutes,
            event_subscription_id=event_subscription_id,
            edge_rule_id=edge_rule_id,
            post_deploy_rollback_window_seconds=post_deploy_rollback_window_seconds,
            enabled=enabled,
            failure_source=failure_source,
            custom_metric_name=custom_metric_name,
            log_query=log_query,
            slo_id=slo_id,
            synthetic_check_id=synthetic_check_id,
            webhook_url=webhook_url,
            channel_ids=channel_ids,
            webhook_secret=webhook_secret,
            cooldown_minutes=cooldown_minutes,
            action=action,
        )

        create_alert_rule_request.additional_properties = d
        return create_alert_rule_request

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> Any:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: Any) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
