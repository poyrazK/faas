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
    comparison: CreateAlertRuleRequestComparison
    threshold: float
    window_spec: CreateAlertRuleRequestWindowSpec
    webhook_url: str
    webhook_secret: str
    """Plaintext HMAC secret (max 256 bytes). Sealed at rest; never echoed."""
    event_subscription_id: UUID | Unset = UNSET
    """Event subscription id in the create alert rule request: immutable subscription selector. Required only for
    event consumer metrics; webhook action and windows up to 24h are required."""
    post_deploy_rollback_window_seconds: int | Unset = UNSET
    """Enable completed-release rollback for this many seconds after cutover; 0 disables it. Requires
    action=rollback. Only deployment-specific error_rate_pct breaches with gt or gte comparisons can qualify."""
    enabled: bool | Unset = UNSET
    failure_source: CreateAlertRuleRequestFailureSource | Unset = UNSET
    """Required when metric == failed_invocations; omit otherwise (xor_chk)."""
    synthetic_check_id: UUID | Unset = UNSET
    """Required for synthetic_check_consecutive_failures (failed runs since the last success; window_spec ignored)
    and synthetic_check_latency_p95_ms (p95 of successful runs in the window); names one of this app's checks."""
    cooldown_minutes: int | Unset = UNSET
    action: CreateAlertRuleRequestAction | Unset = "webhook"
    """What to do when the rule fires. Omit to default to webhook. Pre-auth target and event consumer and workflow
    metrics support webhook only."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        metric: str = self.metric

        comparison: str = self.comparison

        threshold = self.threshold

        window_spec: str = self.window_spec

        webhook_url = self.webhook_url

        webhook_secret = self.webhook_secret

        event_subscription_id: str | Unset = UNSET
        if not isinstance(self.event_subscription_id, Unset):
            event_subscription_id = str(self.event_subscription_id)

        post_deploy_rollback_window_seconds = self.post_deploy_rollback_window_seconds

        enabled = self.enabled

        failure_source: str | Unset = UNSET
        if not isinstance(self.failure_source, Unset):
            failure_source = self.failure_source

        synthetic_check_id: str | Unset = UNSET
        if not isinstance(self.synthetic_check_id, Unset):
            synthetic_check_id = str(self.synthetic_check_id)

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
                "webhook_url": webhook_url,
                "webhook_secret": webhook_secret,
            }
        )
        if event_subscription_id is not UNSET:
            field_dict["event_subscription_id"] = event_subscription_id
        if post_deploy_rollback_window_seconds is not UNSET:
            field_dict["post_deploy_rollback_window_seconds"] = post_deploy_rollback_window_seconds
        if enabled is not UNSET:
            field_dict["enabled"] = enabled
        if failure_source is not UNSET:
            field_dict["failure_source"] = failure_source
        if synthetic_check_id is not UNSET:
            field_dict["synthetic_check_id"] = synthetic_check_id
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

        webhook_url = d.pop("webhook_url")

        webhook_secret = d.pop("webhook_secret")

        _event_subscription_id = d.pop("event_subscription_id", UNSET)
        event_subscription_id: UUID | Unset
        if isinstance(_event_subscription_id, Unset):
            event_subscription_id = UNSET
        else:
            event_subscription_id = UUID(_event_subscription_id)

        post_deploy_rollback_window_seconds = d.pop("post_deploy_rollback_window_seconds", UNSET)

        enabled = d.pop("enabled", UNSET)

        _failure_source = d.pop("failure_source", UNSET)
        failure_source: CreateAlertRuleRequestFailureSource | Unset
        if isinstance(_failure_source, Unset):
            failure_source = UNSET
        else:
            failure_source = check_create_alert_rule_request_failure_source(_failure_source)

        _synthetic_check_id = d.pop("synthetic_check_id", UNSET)
        synthetic_check_id: UUID | Unset
        if isinstance(_synthetic_check_id, Unset):
            synthetic_check_id = UNSET
        else:
            synthetic_check_id = UUID(_synthetic_check_id)

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
            webhook_url=webhook_url,
            webhook_secret=webhook_secret,
            event_subscription_id=event_subscription_id,
            post_deploy_rollback_window_seconds=post_deploy_rollback_window_seconds,
            enabled=enabled,
            failure_source=failure_source,
            synthetic_check_id=synthetic_check_id,
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
