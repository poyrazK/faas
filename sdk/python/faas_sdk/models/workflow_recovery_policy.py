from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.workflow_recovery_policy_failure_statuses_item import (
    WorkflowRecoveryPolicyFailureStatusesItem,
    check_workflow_recovery_policy_failure_statuses_item,
)
from ..models.workflow_recovery_policy_mode import WorkflowRecoveryPolicyMode, check_workflow_recovery_policy_mode
from ..models.workflow_recovery_policy_triggers_item import (
    WorkflowRecoveryPolicyTriggersItem,
    check_workflow_recovery_policy_triggers_item,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.workflow_recovery_health_alert_policy import WorkflowRecoveryHealthAlertPolicy


T = TypeVar("T", bound="WorkflowRecoveryPolicy")


@_attrs_define
class WorkflowRecoveryPolicy:
    """Explicit app recovery policy. Preview prepares parent-job plans and worker-discovered initial-failure reviews.
    initial_failure requires explicit failure statuses. Opt-in automatic_retry requires saved_failure only;
    automatic_initial requires initial_failure only. Both require explicit workflow and failure-status filters and
    execution limits. Resume count caps the absolute generation; preparation and execution budgets are separate.
    Cancellation remains manual.

    """

    enabled: bool
    mode: WorkflowRecoveryPolicyMode
    workflow_names: list[str]
    triggers: list[WorkflowRecoveryPolicyTriggersItem]
    failure_statuses: list[WorkflowRecoveryPolicyFailureStatusesItem]
    max_resume_count: int
    cooldown_seconds: int
    hourly_action_budget: int
    rate_per_second: int
    reason: str
    health_alerts: WorkflowRecoveryHealthAlertPolicy | Unset = UNSET
    """Opt-in recovery health incident notifications with explicit scheduler evaluation and sustained unhealthy
    duration thresholds. Settings are replaced with the full recovery policy. Read-only health query thresholds
    remain request-local."""
    hourly_execution_budget: int | Unset = UNSET
    """Required 1..100 for either automatic mode; zero or omitted in preview. Counts automatic job creation across
    policy versions and automatic modes"""
    pause_after_failures: int | Unset = UNSET
    """Required 1..10 for either automatic mode; zero or omitted in preview. Consecutive proven failures pause
    automatic execution."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        enabled = self.enabled

        mode: str = self.mode

        workflow_names = self.workflow_names

        triggers = []
        for triggers_item_data in self.triggers:
            triggers_item: str = triggers_item_data
            triggers.append(triggers_item)

        failure_statuses = []
        for failure_statuses_item_data in self.failure_statuses:
            failure_statuses_item: str = failure_statuses_item_data
            failure_statuses.append(failure_statuses_item)

        max_resume_count = self.max_resume_count

        cooldown_seconds = self.cooldown_seconds

        hourly_action_budget = self.hourly_action_budget

        rate_per_second = self.rate_per_second

        reason = self.reason

        health_alerts: dict[str, Any] | Unset = UNSET
        if not isinstance(self.health_alerts, Unset):
            health_alerts = self.health_alerts.to_dict()

        hourly_execution_budget = self.hourly_execution_budget

        pause_after_failures = self.pause_after_failures

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "enabled": enabled,
                "mode": mode,
                "workflow_names": workflow_names,
                "triggers": triggers,
                "failure_statuses": failure_statuses,
                "max_resume_count": max_resume_count,
                "cooldown_seconds": cooldown_seconds,
                "hourly_action_budget": hourly_action_budget,
                "rate_per_second": rate_per_second,
                "reason": reason,
            }
        )
        if health_alerts is not UNSET:
            field_dict["health_alerts"] = health_alerts
        if hourly_execution_budget is not UNSET:
            field_dict["hourly_execution_budget"] = hourly_execution_budget
        if pause_after_failures is not UNSET:
            field_dict["pause_after_failures"] = pause_after_failures

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.workflow_recovery_health_alert_policy import WorkflowRecoveryHealthAlertPolicy

        d = dict(src_dict)
        enabled = d.pop("enabled")

        mode = check_workflow_recovery_policy_mode(d.pop("mode"))

        workflow_names = cast(list[str], d.pop("workflow_names"))

        triggers = []
        _triggers = d.pop("triggers")
        for triggers_item_data in _triggers:
            triggers_item = check_workflow_recovery_policy_triggers_item(triggers_item_data)

            triggers.append(triggers_item)

        failure_statuses = []
        _failure_statuses = d.pop("failure_statuses")
        for failure_statuses_item_data in _failure_statuses:
            failure_statuses_item = check_workflow_recovery_policy_failure_statuses_item(failure_statuses_item_data)

            failure_statuses.append(failure_statuses_item)

        max_resume_count = d.pop("max_resume_count")

        cooldown_seconds = d.pop("cooldown_seconds")

        hourly_action_budget = d.pop("hourly_action_budget")

        rate_per_second = d.pop("rate_per_second")

        reason = d.pop("reason")

        _health_alerts = d.pop("health_alerts", UNSET)
        health_alerts: WorkflowRecoveryHealthAlertPolicy | Unset
        if isinstance(_health_alerts, Unset):
            health_alerts = UNSET
        else:
            health_alerts = WorkflowRecoveryHealthAlertPolicy.from_dict(_health_alerts)

        hourly_execution_budget = d.pop("hourly_execution_budget", UNSET)

        pause_after_failures = d.pop("pause_after_failures", UNSET)

        workflow_recovery_policy = cls(
            enabled=enabled,
            mode=mode,
            workflow_names=workflow_names,
            triggers=triggers,
            failure_statuses=failure_statuses,
            max_resume_count=max_resume_count,
            cooldown_seconds=cooldown_seconds,
            hourly_action_budget=hourly_action_budget,
            rate_per_second=rate_per_second,
            reason=reason,
            health_alerts=health_alerts,
            hourly_execution_budget=hourly_execution_budget,
            pause_after_failures=pause_after_failures,
        )

        workflow_recovery_policy.additional_properties = d
        return workflow_recovery_policy

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
