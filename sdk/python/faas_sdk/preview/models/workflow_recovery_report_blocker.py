from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.workflow_recovery_history_entry import WorkflowRecoveryHistoryEntry


T = TypeVar("T", bound="WorkflowRecoveryReportBlocker")


@_attrs_define
class WorkflowRecoveryReportBlocker:
    """Distinct actions affected by an observed worker blocker with bounded source evidence."""

    reason: str
    actions: int
    evidence: list[WorkflowRecoveryHistoryEntry]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        reason = self.reason

        actions = self.actions

        evidence = []
        for evidence_item_data in self.evidence:
            evidence_item = evidence_item_data.to_dict()
            evidence.append(evidence_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "reason": reason,
                "actions": actions,
                "evidence": evidence,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.workflow_recovery_history_entry import WorkflowRecoveryHistoryEntry

        d = dict(src_dict)
        reason = d.pop("reason")

        actions = d.pop("actions")

        evidence = []
        _evidence = d.pop("evidence")
        for evidence_item_data in _evidence:
            evidence_item = WorkflowRecoveryHistoryEntry.from_dict(evidence_item_data)

            evidence.append(evidence_item)

        workflow_recovery_report_blocker = cls(
            reason=reason,
            actions=actions,
            evidence=evidence,
        )

        workflow_recovery_report_blocker.additional_properties = d
        return workflow_recovery_report_blocker

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
