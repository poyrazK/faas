from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.workflow_recovery_policy_change_entry_effect import (
    WorkflowRecoveryPolicyChangeEntryEffect,
    check_workflow_recovery_policy_change_entry_effect,
)

if TYPE_CHECKING:
    from ..models.workflow_recovery_automatic_queue_entry import WorkflowRecoveryAutomaticQueueEntry


T = TypeVar("T", bound="WorkflowRecoveryPolicyChangeEntry")


@_attrs_define
class WorkflowRecoveryPolicyChangeEntry:
    """Retained automatic queue entry and the effect of replacing its policy configuration."""

    entry: WorkflowRecoveryAutomaticQueueEntry
    """One pending or unresolved applied automatic retry. Queued_at is the frozen review preparation time. Ages are
    whole nonnegative seconds relative to the queue observed_at. Waiting age is omitted until the worker records a
    wait; the saved reason may be historical."""
    effect: WorkflowRecoveryPolicyChangeEntryEffect
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        entry = self.entry.to_dict()

        effect: str = self.effect

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "entry": entry,
                "effect": effect,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.workflow_recovery_automatic_queue_entry import WorkflowRecoveryAutomaticQueueEntry

        d = dict(src_dict)
        entry = WorkflowRecoveryAutomaticQueueEntry.from_dict(d.pop("entry"))

        effect = check_workflow_recovery_policy_change_entry_effect(d.pop("effect"))

        workflow_recovery_policy_change_entry = cls(
            entry=entry,
            effect=effect,
        )

        workflow_recovery_policy_change_entry.additional_properties = d
        return workflow_recovery_policy_change_entry

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
