from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.update_alert_rule_request_action import (
    UpdateAlertRuleRequestAction,
    check_update_alert_rule_request_action,
)
from ..models.update_alert_rule_request_comparison import (
    UpdateAlertRuleRequestComparison,
    check_update_alert_rule_request_comparison,
)
from ..models.update_alert_rule_request_metric import (
    UpdateAlertRuleRequestMetric,
    check_update_alert_rule_request_metric,
)
from ..models.update_alert_rule_request_window_spec import (
    UpdateAlertRuleRequestWindowSpec,
    check_update_alert_rule_request_window_spec,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="UpdateAlertRuleRequest")


@_attrs_define
class UpdateAlertRuleRequest:
    """Partial update — every field is optional. Omitted means leave alone."""

    post_deploy_rollback_window_seconds: int | Unset = UNSET
    """Change the completed-release recovery window. Omission preserves it and explicit 0 disables it. Positive
    values require action=rollback; only error_rate_pct with gt or gte and sufficient exact deployment telemetry
    qualifies."""
    name: str | Unset = UNSET
    enabled: bool | Unset = UNSET
    metric: UpdateAlertRuleRequestMetric | Unset = UNSET
    """Cannot cross metric families (e.g. error_rate_pct → failed_invocations) — returns 400."""
    comparison: UpdateAlertRuleRequestComparison | Unset = UNSET
    threshold: float | Unset = UNSET
    window_spec: UpdateAlertRuleRequestWindowSpec | Unset = UNSET
    webhook_url: str | Unset = UNSET
    webhook_secret: str | Unset = UNSET
    """New plaintext HMAC secret. Omit to keep the existing secret."""
    cooldown_minutes: int | Unset = UNSET
    action: UpdateAlertRuleRequestAction | Unset = UNSET
    """Replace the action. Omit to leave the existing action in place. Pre-auth target and event consumer metrics
    support webhook only."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        post_deploy_rollback_window_seconds = self.post_deploy_rollback_window_seconds

        name = self.name

        enabled = self.enabled

        metric: str | Unset = UNSET
        if not isinstance(self.metric, Unset):
            metric = self.metric

        comparison: str | Unset = UNSET
        if not isinstance(self.comparison, Unset):
            comparison = self.comparison

        threshold = self.threshold

        window_spec: str | Unset = UNSET
        if not isinstance(self.window_spec, Unset):
            window_spec = self.window_spec

        webhook_url = self.webhook_url

        webhook_secret = self.webhook_secret

        cooldown_minutes = self.cooldown_minutes

        action: str | Unset = UNSET
        if not isinstance(self.action, Unset):
            action = self.action

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if post_deploy_rollback_window_seconds is not UNSET:
            field_dict["post_deploy_rollback_window_seconds"] = post_deploy_rollback_window_seconds
        if name is not UNSET:
            field_dict["name"] = name
        if enabled is not UNSET:
            field_dict["enabled"] = enabled
        if metric is not UNSET:
            field_dict["metric"] = metric
        if comparison is not UNSET:
            field_dict["comparison"] = comparison
        if threshold is not UNSET:
            field_dict["threshold"] = threshold
        if window_spec is not UNSET:
            field_dict["window_spec"] = window_spec
        if webhook_url is not UNSET:
            field_dict["webhook_url"] = webhook_url
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
        post_deploy_rollback_window_seconds = d.pop("post_deploy_rollback_window_seconds", UNSET)

        name = d.pop("name", UNSET)

        enabled = d.pop("enabled", UNSET)

        _metric = d.pop("metric", UNSET)
        metric: UpdateAlertRuleRequestMetric | Unset
        if isinstance(_metric, Unset):
            metric = UNSET
        else:
            metric = check_update_alert_rule_request_metric(_metric)

        _comparison = d.pop("comparison", UNSET)
        comparison: UpdateAlertRuleRequestComparison | Unset
        if isinstance(_comparison, Unset):
            comparison = UNSET
        else:
            comparison = check_update_alert_rule_request_comparison(_comparison)

        threshold = d.pop("threshold", UNSET)

        _window_spec = d.pop("window_spec", UNSET)
        window_spec: UpdateAlertRuleRequestWindowSpec | Unset
        if isinstance(_window_spec, Unset):
            window_spec = UNSET
        else:
            window_spec = check_update_alert_rule_request_window_spec(_window_spec)

        webhook_url = d.pop("webhook_url", UNSET)

        webhook_secret = d.pop("webhook_secret", UNSET)

        cooldown_minutes = d.pop("cooldown_minutes", UNSET)

        _action = d.pop("action", UNSET)
        action: UpdateAlertRuleRequestAction | Unset
        if isinstance(_action, Unset):
            action = UNSET
        else:
            action = check_update_alert_rule_request_action(_action)

        update_alert_rule_request = cls(
            post_deploy_rollback_window_seconds=post_deploy_rollback_window_seconds,
            name=name,
            enabled=enabled,
            metric=metric,
            comparison=comparison,
            threshold=threshold,
            window_spec=window_spec,
            webhook_url=webhook_url,
            webhook_secret=webhook_secret,
            cooldown_minutes=cooldown_minutes,
            action=action,
        )

        update_alert_rule_request.additional_properties = d
        return update_alert_rule_request

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
