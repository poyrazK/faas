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
    event_subscription_id: UUID | Unset = UNSET
    """Event subscription id in the create alert rule request: immutable subscription selector. Required only for
    event consumer metrics; webhook action and windows up to 24h are required."""
    post_deploy_rollback_window_seconds: int | Unset = UNSET
    """Enable completed-release rollback for this many seconds after cutover; 0 disables it. Requires
    action=rollback. Only deployment-specific error_rate_pct breaches with gt or gte comparisons can qualify."""
    enabled: bool | Unset = UNSET
    failure_source: CreateAlertRuleRequestFailureSource | Unset = UNSET
    """Required when metric == failed_invocations; omit otherwise (xor_chk)."""
    webhook_url: str | Unset = UNSET
    """Signed webhook destination. Required with webhook_secret unless channel_ids is set."""
    channel_ids: list[UUID] | Unset = UNSET
    """Notification channels (ADR-749) to deliver fires and resolves to. With at least one, webhook_url and
    webhook_secret may be omitted."""
    webhook_secret: str | Unset = UNSET
    """Plaintext HMAC secret (max 256 bytes). Sealed at rest; never echoed."""
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

        event_subscription_id: str | Unset = UNSET
        if not isinstance(self.event_subscription_id, Unset):
            event_subscription_id = str(self.event_subscription_id)

        post_deploy_rollback_window_seconds = self.post_deploy_rollback_window_seconds

        enabled = self.enabled

        failure_source: str | Unset = UNSET
        if not isinstance(self.failure_source, Unset):
            failure_source = self.failure_source

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
        if event_subscription_id is not UNSET:
            field_dict["event_subscription_id"] = event_subscription_id
        if post_deploy_rollback_window_seconds is not UNSET:
            field_dict["post_deploy_rollback_window_seconds"] = post_deploy_rollback_window_seconds
        if enabled is not UNSET:
            field_dict["enabled"] = enabled
        if failure_source is not UNSET:
            field_dict["failure_source"] = failure_source
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
            event_subscription_id=event_subscription_id,
            post_deploy_rollback_window_seconds=post_deploy_rollback_window_seconds,
            enabled=enabled,
            failure_source=failure_source,
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
