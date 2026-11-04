from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.workflow_initial_evidence_repair_receipt import WorkflowInitialEvidenceRepairReceipt


T = TypeVar("T", bound="WorkflowRecoveryPolicyReviewParentEvidenceRepairs")


@_attrs_define
class WorkflowRecoveryPolicyReviewParentEvidenceRepairs:
    additional_properties: dict[str, WorkflowInitialEvidenceRepairReceipt] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:

        field_dict: dict[str, Any] = {}
        for prop_name, prop in self.additional_properties.items():
            field_dict[prop_name] = prop.to_dict()

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.workflow_initial_evidence_repair_receipt import WorkflowInitialEvidenceRepairReceipt

        d = dict(src_dict)
        workflow_recovery_policy_review_parent_evidence_repairs = cls()

        additional_properties = {}
        for prop_name, prop_dict in d.items():
            additional_property = WorkflowInitialEvidenceRepairReceipt.from_dict(prop_dict)

            additional_properties[prop_name] = additional_property

        workflow_recovery_policy_review_parent_evidence_repairs.additional_properties = additional_properties
        return workflow_recovery_policy_review_parent_evidence_repairs

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> WorkflowInitialEvidenceRepairReceipt:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: WorkflowInitialEvidenceRepairReceipt) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
