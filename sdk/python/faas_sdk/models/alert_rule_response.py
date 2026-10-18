from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.alert_rule_response_action import AlertRuleResponseAction, check_alert_rule_response_action
from ..models.alert_rule_response_comparison import AlertRuleResponseComparison, check_alert_rule_response_comparison
from ..models.alert_rule_response_failure_source import (
    AlertRuleResponseFailureSource,
    check_alert_rule_response_failure_source,
)
from ..models.alert_rule_response_metric import AlertRuleResponseMetric, check_alert_rule_response_metric
from ..models.alert_rule_response_state import AlertRuleResponseState, check_alert_rule_response_state
from ..models.alert_rule_response_window_spec import AlertRuleResponseWindowSpec, check_alert_rule_response_window_spec
from ..types import UNSET, Unset

T = TypeVar("T", bound="AlertRuleResponse")


@_attrs_define
class AlertRuleResponse:
    """A customer-configurable alert rule. Carries the masked webhook
    secret; the sealed ciphertext is server-side only.

    """

    id: str
    app_id: str
    """Pinned app id. Empty string = account-wide rule."""
    name: str
    enabled: bool
    metric: AlertRuleResponseMetric
    """MCP task age reads a fresh app gauge in seconds and degrades when unavailable; mcp_tasks_metrics_stale is 1
    when either task gauge is missing, stale, or invalid. Min=0 worker apps also require a fresh observer heartbeat
    with value 1. mcp_task_promotion_hold_age_seconds reads the oldest active hold directly from state (seconds,
    zero with no holds); its alerts support webhook action only."""
    comparison: AlertRuleResponseComparison
    threshold: float
    window_spec: AlertRuleResponseWindowSpec
    webhook_url: str
    """Signed webhook destination; empty for a rule that delivers only to notification channels."""
    webhook_secret_sealed_masked: str
    """Literal "***" — the plaintext is never returned."""
    cooldown_minutes: int
    state: AlertRuleResponseState
    """Evaluation state. degraded means the rule's own metric source is unavailable; unknown means insufficient
    samples, compacted event history, or a paused event consumer."""
    created_at: datetime.datetime
    updated_at: datetime.datetime
    action: AlertRuleResponseAction = "webhook"
    """What to do when the rule fires. webhook = fire the configured webhook only (legacy default). rollback = roll
    the rule's app back to its last live deployment. demote = pin the current canary step (no traffic advance).
    promote = short-circuit the canary ladder to 100%. Pre-auth target and MCP task, event consumer, and workflow
    metrics support webhook only."""
    event_subscription_id: UUID | Unset = UNSET
    """Immutable subscription selector. Required only for event consumer metrics; webhook action and windows up to
    24h are required."""
    edge_rule_id: UUID | Unset = UNSET
    """ADR-1192. Immutable edge rule selector for edge_rule_hits and edge_rule_log_hits; absent means every rule of
    the app."""
    post_deploy_rollback_window_seconds: int | Unset = UNSET
    """Configured completed-release rollback eligibility window in seconds; 0 is disabled. Acceptance requires
    deployment-specific error_rate_pct evidence, a gt or gte comparison, and recorded predecessor lineage."""
    failure_source: AlertRuleResponseFailureSource | Unset = UNSET
    """Source dimension for failed_invocations; omit when metric is not failed_invocations (xor_chk)."""
    custom_metric_name: str | Unset = UNSET
    """The pushed custom metric a custom_metric rule watches (ADR-745). Present only when metric is custom_metric."""
    slo_id: UUID | Unset = UNSET
    """The customer SLO an slo_budget_burn or slo_budget_remaining_pct rule watches (ADR-747); absent for other
    metrics."""
    synthetic_check_id: UUID | Unset = UNSET
    """The synthetic check a synthetic_check_* rule watches (ADR-748); absent for other metrics."""
    channel_ids: list[UUID] | Unset = UNSET
    """Notification channels (ADR-749) the rule delivers fires and resolves to."""
    last_fired_at: datetime.datetime | Unset = UNSET
    last_evaluated_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        app_id = self.app_id

        name = self.name

        enabled = self.enabled

        metric: str = self.metric

        comparison: str = self.comparison

        threshold = self.threshold

        window_spec: str = self.window_spec

        webhook_url = self.webhook_url

        webhook_secret_sealed_masked = self.webhook_secret_sealed_masked

        cooldown_minutes = self.cooldown_minutes

        action: str = self.action

        state: str = self.state

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        event_subscription_id: str | Unset = UNSET
        if not isinstance(self.event_subscription_id, Unset):
            event_subscription_id = str(self.event_subscription_id)

        edge_rule_id: str | Unset = UNSET
        if not isinstance(self.edge_rule_id, Unset):
            edge_rule_id = str(self.edge_rule_id)

        post_deploy_rollback_window_seconds = self.post_deploy_rollback_window_seconds

        failure_source: str | Unset = UNSET
        if not isinstance(self.failure_source, Unset):
            failure_source = self.failure_source

        custom_metric_name = self.custom_metric_name

        slo_id: str | Unset = UNSET
        if not isinstance(self.slo_id, Unset):
            slo_id = str(self.slo_id)

        synthetic_check_id: str | Unset = UNSET
        if not isinstance(self.synthetic_check_id, Unset):
            synthetic_check_id = str(self.synthetic_check_id)

        channel_ids: list[str] | Unset = UNSET
        if not isinstance(self.channel_ids, Unset):
            channel_ids = []
            for channel_ids_item_data in self.channel_ids:
                channel_ids_item = str(channel_ids_item_data)
                channel_ids.append(channel_ids_item)

        last_fired_at: str | Unset = UNSET
        if not isinstance(self.last_fired_at, Unset):
            last_fired_at = self.last_fired_at.isoformat()

        last_evaluated_at: str | Unset = UNSET
        if not isinstance(self.last_evaluated_at, Unset):
            last_evaluated_at = self.last_evaluated_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "app_id": app_id,
                "name": name,
                "enabled": enabled,
                "metric": metric,
                "comparison": comparison,
                "threshold": threshold,
                "window_spec": window_spec,
                "webhook_url": webhook_url,
                "webhook_secret_sealed_masked": webhook_secret_sealed_masked,
                "cooldown_minutes": cooldown_minutes,
                "action": action,
                "state": state,
                "created_at": created_at,
                "updated_at": updated_at,
            }
        )
        if event_subscription_id is not UNSET:
            field_dict["event_subscription_id"] = event_subscription_id
        if edge_rule_id is not UNSET:
            field_dict["edge_rule_id"] = edge_rule_id
        if post_deploy_rollback_window_seconds is not UNSET:
            field_dict["post_deploy_rollback_window_seconds"] = post_deploy_rollback_window_seconds
        if failure_source is not UNSET:
            field_dict["failure_source"] = failure_source
        if custom_metric_name is not UNSET:
            field_dict["custom_metric_name"] = custom_metric_name
        if slo_id is not UNSET:
            field_dict["slo_id"] = slo_id
        if synthetic_check_id is not UNSET:
            field_dict["synthetic_check_id"] = synthetic_check_id
        if channel_ids is not UNSET:
            field_dict["channel_ids"] = channel_ids
        if last_fired_at is not UNSET:
            field_dict["last_fired_at"] = last_fired_at
        if last_evaluated_at is not UNSET:
            field_dict["last_evaluated_at"] = last_evaluated_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = d.pop("id")

        app_id = d.pop("app_id")

        name = d.pop("name")

        enabled = d.pop("enabled")

        metric = check_alert_rule_response_metric(d.pop("metric"))

        comparison = check_alert_rule_response_comparison(d.pop("comparison"))

        threshold = d.pop("threshold")

        window_spec = check_alert_rule_response_window_spec(d.pop("window_spec"))

        webhook_url = d.pop("webhook_url")

        webhook_secret_sealed_masked = d.pop("webhook_secret_sealed_masked")

        cooldown_minutes = d.pop("cooldown_minutes")

        action = check_alert_rule_response_action(d.pop("action"))

        state = check_alert_rule_response_state(d.pop("state"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

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

        _failure_source = d.pop("failure_source", UNSET)
        failure_source: AlertRuleResponseFailureSource | Unset
        if isinstance(_failure_source, Unset):
            failure_source = UNSET
        else:
            failure_source = check_alert_rule_response_failure_source(_failure_source)

        custom_metric_name = d.pop("custom_metric_name", UNSET)

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

        _channel_ids = d.pop("channel_ids", UNSET)
        channel_ids: list[UUID] | Unset = UNSET
        if _channel_ids is not UNSET:
            channel_ids = []
            for channel_ids_item_data in _channel_ids:
                channel_ids_item = UUID(channel_ids_item_data)

                channel_ids.append(channel_ids_item)

        _last_fired_at = d.pop("last_fired_at", UNSET)
        last_fired_at: datetime.datetime | Unset
        if isinstance(_last_fired_at, Unset):
            last_fired_at = UNSET
        else:
            last_fired_at = datetime.datetime.fromisoformat(_last_fired_at)

        _last_evaluated_at = d.pop("last_evaluated_at", UNSET)
        last_evaluated_at: datetime.datetime | Unset
        if isinstance(_last_evaluated_at, Unset):
            last_evaluated_at = UNSET
        else:
            last_evaluated_at = datetime.datetime.fromisoformat(_last_evaluated_at)

        alert_rule_response = cls(
            id=id,
            app_id=app_id,
            name=name,
            enabled=enabled,
            metric=metric,
            comparison=comparison,
            threshold=threshold,
            window_spec=window_spec,
            webhook_url=webhook_url,
            webhook_secret_sealed_masked=webhook_secret_sealed_masked,
            cooldown_minutes=cooldown_minutes,
            action=action,
            state=state,
            created_at=created_at,
            updated_at=updated_at,
            event_subscription_id=event_subscription_id,
            edge_rule_id=edge_rule_id,
            post_deploy_rollback_window_seconds=post_deploy_rollback_window_seconds,
            failure_source=failure_source,
            custom_metric_name=custom_metric_name,
            slo_id=slo_id,
            synthetic_check_id=synthetic_check_id,
            channel_ids=channel_ids,
            last_fired_at=last_fired_at,
            last_evaluated_at=last_evaluated_at,
        )

        alert_rule_response.additional_properties = d
        return alert_rule_response

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
