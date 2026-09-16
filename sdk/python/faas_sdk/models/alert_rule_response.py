from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

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
    comparison: AlertRuleResponseComparison
    threshold: float
    window_spec: AlertRuleResponseWindowSpec
    webhook_url: str
    webhook_secret_sealed_masked: str
    """Literal "***" — the plaintext is never returned."""
    cooldown_minutes: int
    state: AlertRuleResponseState
    """Evaluation state. degraded means the rule's own metric source is unavailable."""
    created_at: datetime.datetime
    updated_at: datetime.datetime
    action: AlertRuleResponseAction = "webhook"
    """What to do when the rule fires. webhook = fire the configured webhook only (legacy default). rollback = roll
    the rule's app back to its last live deployment. demote = pin the current canary step (no traffic advance).
    promote = short-circuit the canary ladder to 100%."""
    failure_source: AlertRuleResponseFailureSource | Unset = UNSET
    """Source dimension for failed_invocations; omit when metric is not failed_invocations (xor_chk)."""
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

        failure_source: str | Unset = UNSET
        if not isinstance(self.failure_source, Unset):
            failure_source = self.failure_source

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
        if failure_source is not UNSET:
            field_dict["failure_source"] = failure_source
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

        _failure_source = d.pop("failure_source", UNSET)
        failure_source: AlertRuleResponseFailureSource | Unset
        if isinstance(_failure_source, Unset):
            failure_source = UNSET
        else:
            failure_source = check_alert_rule_response_failure_source(_failure_source)

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
            failure_source=failure_source,
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
