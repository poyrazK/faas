from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.workflow_recovery_policy_health_status import (
    WorkflowRecoveryPolicyHealthStatus,
    check_workflow_recovery_policy_health_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.workflow_recovery_automatic_queue import WorkflowRecoveryAutomaticQueue
    from ..models.workflow_recovery_overview import WorkflowRecoveryOverview


T = TypeVar("T", bound="WorkflowRecoveryPolicyHealth")


@_attrs_define
class WorkflowRecoveryPolicyHealth:
    """Advisory recovery automation health with bounded queue evidence. Status precedence is paused, stalled, observed
    blockers, current budget or cooldown, incomplete sample, then inactive or healthy. Sample progress is only from
    retained queue entries and is not a durable app-wide last-success timestamp. Healthy means no blocker observed in
    this snapshot, not scheduler or platform liveness.

    """

    status: WorkflowRecoveryPolicyHealthStatus
    reason: str
    stall_after_seconds: int
    queue: WorkflowRecoveryAutomaticQueue
    """Informational automatic retry queue snapshot. Outstanding means a child has been created but final proof has
    not been recorded; it does not assert the child is currently running. Pending counts current policy intents
    only. Charges survive review pruning and policy replacement. Paused policies retain their queue and committed
    children continue. Missing evidence is unresolved, never success or failure."""
    overview: WorkflowRecoveryOverview | Unset = UNSET
    """Read-only retained recovery observations from one snapshot. Covers manual initial reviews, manual retry
    disposition/completion observations and the automatic queue, not all workflow outcomes or scheduler liveness.
    Counts are exact; issues are bounded. Delivery covers the last retained polling alert only."""
    sample_last_progress_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        status: str = self.status

        reason = self.reason

        stall_after_seconds = self.stall_after_seconds

        queue = self.queue.to_dict()

        overview: dict[str, Any] | Unset = UNSET
        if not isinstance(self.overview, Unset):
            overview = self.overview.to_dict()

        sample_last_progress_at: str | Unset = UNSET
        if not isinstance(self.sample_last_progress_at, Unset):
            sample_last_progress_at = self.sample_last_progress_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "status": status,
                "reason": reason,
                "stall_after_seconds": stall_after_seconds,
                "queue": queue,
            }
        )
        if overview is not UNSET:
            field_dict["overview"] = overview
        if sample_last_progress_at is not UNSET:
            field_dict["sample_last_progress_at"] = sample_last_progress_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.workflow_recovery_automatic_queue import WorkflowRecoveryAutomaticQueue
        from ..models.workflow_recovery_overview import WorkflowRecoveryOverview

        d = dict(src_dict)
        status = check_workflow_recovery_policy_health_status(d.pop("status"))

        reason = d.pop("reason")

        stall_after_seconds = d.pop("stall_after_seconds")

        queue = WorkflowRecoveryAutomaticQueue.from_dict(d.pop("queue"))

        _overview = d.pop("overview", UNSET)
        overview: WorkflowRecoveryOverview | Unset
        if isinstance(_overview, Unset):
            overview = UNSET
        else:
            overview = WorkflowRecoveryOverview.from_dict(_overview)

        _sample_last_progress_at = d.pop("sample_last_progress_at", UNSET)
        sample_last_progress_at: datetime.datetime | Unset
        if isinstance(_sample_last_progress_at, Unset):
            sample_last_progress_at = UNSET
        else:
            sample_last_progress_at = datetime.datetime.fromisoformat(_sample_last_progress_at)

        workflow_recovery_policy_health = cls(
            status=status,
            reason=reason,
            stall_after_seconds=stall_after_seconds,
            queue=queue,
            overview=overview,
            sample_last_progress_at=sample_last_progress_at,
        )

        workflow_recovery_policy_health.additional_properties = d
        return workflow_recovery_policy_health

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
