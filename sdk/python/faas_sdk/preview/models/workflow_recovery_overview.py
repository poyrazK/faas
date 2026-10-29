from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.workflow_recovery_overview_monitoring_status import (
    WorkflowRecoveryOverviewMonitoringStatus,
    check_workflow_recovery_overview_monitoring_status,
)
from ..models.workflow_recovery_overview_status import (
    WorkflowRecoveryOverviewStatus,
    check_workflow_recovery_overview_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.workflow_recovery_overview_alert import WorkflowRecoveryOverviewAlert
    from ..models.workflow_recovery_overview_issue import WorkflowRecoveryOverviewIssue


T = TypeVar("T", bound="WorkflowRecoveryOverview")


@_attrs_define
class WorkflowRecoveryOverview:
    """Read-only retained recovery observations from one snapshot. Covers manual initial reviews, manual retry
    disposition/completion observations and the automatic queue, not all workflow outcomes or scheduler liveness. Counts
    are exact; issues are bounded. Delivery covers the last retained polling alert only.

    """

    status: WorkflowRecoveryOverviewStatus
    monitoring_status: WorkflowRecoveryOverviewMonitoringStatus
    observed_at: datetime.datetime
    monitoring_only: bool
    manual_awaiting_approval: int
    manual_unresolved: int
    manual_polling_failures: int
    manual_attention: int
    truncated: bool
    issues: list[WorkflowRecoveryOverviewIssue]
    manual_retry_awaiting_review: int | Unset = UNSET
    """Undecided current enabled preview retry plans requiring a live status inspection; no freshness or readiness
    guarantee."""
    manual_retry_unresolved: int | Unset = UNSET
    """Approved retry reviews without a saved completion observation, including unapplied plans and reviews from
    older policy versions."""
    last_alert: WorkflowRecoveryOverviewAlert | Unset = UNSET
    """Delivery observations for frozen receipt receivers. Missing deliveries without an outbox entry are unknown.
    No receivers is not delivered."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        status: str = self.status

        monitoring_status: str = self.monitoring_status

        observed_at = self.observed_at.isoformat()

        monitoring_only = self.monitoring_only

        manual_awaiting_approval = self.manual_awaiting_approval

        manual_unresolved = self.manual_unresolved

        manual_polling_failures = self.manual_polling_failures

        manual_attention = self.manual_attention

        truncated = self.truncated

        issues = []
        for issues_item_data in self.issues:
            issues_item = issues_item_data.to_dict()
            issues.append(issues_item)

        manual_retry_awaiting_review = self.manual_retry_awaiting_review

        manual_retry_unresolved = self.manual_retry_unresolved

        last_alert: dict[str, Any] | Unset = UNSET
        if not isinstance(self.last_alert, Unset):
            last_alert = self.last_alert.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "status": status,
                "monitoring_status": monitoring_status,
                "observed_at": observed_at,
                "monitoring_only": monitoring_only,
                "manual_awaiting_approval": manual_awaiting_approval,
                "manual_unresolved": manual_unresolved,
                "manual_polling_failures": manual_polling_failures,
                "manual_attention": manual_attention,
                "truncated": truncated,
                "issues": issues,
            }
        )
        if manual_retry_awaiting_review is not UNSET:
            field_dict["manual_retry_awaiting_review"] = manual_retry_awaiting_review
        if manual_retry_unresolved is not UNSET:
            field_dict["manual_retry_unresolved"] = manual_retry_unresolved
        if last_alert is not UNSET:
            field_dict["last_alert"] = last_alert

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.workflow_recovery_overview_alert import WorkflowRecoveryOverviewAlert
        from ..models.workflow_recovery_overview_issue import WorkflowRecoveryOverviewIssue

        d = dict(src_dict)
        status = check_workflow_recovery_overview_status(d.pop("status"))

        monitoring_status = check_workflow_recovery_overview_monitoring_status(d.pop("monitoring_status"))

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        monitoring_only = d.pop("monitoring_only")

        manual_awaiting_approval = d.pop("manual_awaiting_approval")

        manual_unresolved = d.pop("manual_unresolved")

        manual_polling_failures = d.pop("manual_polling_failures")

        manual_attention = d.pop("manual_attention")

        truncated = d.pop("truncated")

        issues = []
        _issues = d.pop("issues")
        for issues_item_data in _issues:
            issues_item = WorkflowRecoveryOverviewIssue.from_dict(issues_item_data)

            issues.append(issues_item)

        manual_retry_awaiting_review = d.pop("manual_retry_awaiting_review", UNSET)

        manual_retry_unresolved = d.pop("manual_retry_unresolved", UNSET)

        _last_alert = d.pop("last_alert", UNSET)
        last_alert: WorkflowRecoveryOverviewAlert | Unset
        if isinstance(_last_alert, Unset):
            last_alert = UNSET
        else:
            last_alert = WorkflowRecoveryOverviewAlert.from_dict(_last_alert)

        workflow_recovery_overview = cls(
            status=status,
            monitoring_status=monitoring_status,
            observed_at=observed_at,
            monitoring_only=monitoring_only,
            manual_awaiting_approval=manual_awaiting_approval,
            manual_unresolved=manual_unresolved,
            manual_polling_failures=manual_polling_failures,
            manual_attention=manual_attention,
            truncated=truncated,
            issues=issues,
            manual_retry_awaiting_review=manual_retry_awaiting_review,
            manual_retry_unresolved=manual_retry_unresolved,
            last_alert=last_alert,
        )

        workflow_recovery_overview.additional_properties = d
        return workflow_recovery_overview

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
