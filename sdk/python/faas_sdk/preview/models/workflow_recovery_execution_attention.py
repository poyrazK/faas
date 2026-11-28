from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.workflow_recovery_execution_attention_target import WorkflowRecoveryExecutionAttentionTarget


T = TypeVar("T", bound="WorkflowRecoveryExecutionAttention")


@_attrs_define
class WorkflowRecoveryExecutionAttention:
    """Generation-fenced advisory execution monitoring. Capacity and scheduled wait counters describe underlying waits and
    may overlap deadline exceeded counts. Only the first attention event per job is captured. Detection never cancels or
    retries work.

    """

    observed_at: datetime.datetime
    deadline_seconds: int
    stall_seconds: int
    stalled_count: int
    deadline_exceeded_count: int
    capacity_waiting_count: int
    scheduled_waiting_count: int
    unknown_count: int
    needs_attention: bool
    notification_captured: bool
    targets: list[WorkflowRecoveryExecutionAttentionTarget]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        observed_at = self.observed_at.isoformat()

        deadline_seconds = self.deadline_seconds

        stall_seconds = self.stall_seconds

        stalled_count = self.stalled_count

        deadline_exceeded_count = self.deadline_exceeded_count

        capacity_waiting_count = self.capacity_waiting_count

        scheduled_waiting_count = self.scheduled_waiting_count

        unknown_count = self.unknown_count

        needs_attention = self.needs_attention

        notification_captured = self.notification_captured

        targets = []
        for targets_item_data in self.targets:
            targets_item = targets_item_data.to_dict()
            targets.append(targets_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "observed_at": observed_at,
                "deadline_seconds": deadline_seconds,
                "stall_seconds": stall_seconds,
                "stalled_count": stalled_count,
                "deadline_exceeded_count": deadline_exceeded_count,
                "capacity_waiting_count": capacity_waiting_count,
                "scheduled_waiting_count": scheduled_waiting_count,
                "unknown_count": unknown_count,
                "needs_attention": needs_attention,
                "notification_captured": notification_captured,
                "targets": targets,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.workflow_recovery_execution_attention_target import WorkflowRecoveryExecutionAttentionTarget

        d = dict(src_dict)
        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        deadline_seconds = d.pop("deadline_seconds")

        stall_seconds = d.pop("stall_seconds")

        stalled_count = d.pop("stalled_count")

        deadline_exceeded_count = d.pop("deadline_exceeded_count")

        capacity_waiting_count = d.pop("capacity_waiting_count")

        scheduled_waiting_count = d.pop("scheduled_waiting_count")

        unknown_count = d.pop("unknown_count")

        needs_attention = d.pop("needs_attention")

        notification_captured = d.pop("notification_captured")

        targets = []
        _targets = d.pop("targets")
        for targets_item_data in _targets:
            targets_item = WorkflowRecoveryExecutionAttentionTarget.from_dict(targets_item_data)

            targets.append(targets_item)

        workflow_recovery_execution_attention = cls(
            observed_at=observed_at,
            deadline_seconds=deadline_seconds,
            stall_seconds=stall_seconds,
            stalled_count=stalled_count,
            deadline_exceeded_count=deadline_exceeded_count,
            capacity_waiting_count=capacity_waiting_count,
            scheduled_waiting_count=scheduled_waiting_count,
            unknown_count=unknown_count,
            needs_attention=needs_attention,
            notification_captured=notification_captured,
            targets=targets,
        )

        workflow_recovery_execution_attention.additional_properties = d
        return workflow_recovery_execution_attention

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
