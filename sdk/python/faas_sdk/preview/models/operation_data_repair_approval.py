from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.operation_data_repair_approval_decision import (
    OperationDataRepairApprovalDecision,
    check_operation_data_repair_approval_decision,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_data_repair_approval_actor import OperationDataRepairApprovalActor


T = TypeVar("T", bound="OperationDataRepairApproval")


@_attrs_define
class OperationDataRepairApproval:
    step: str
    decision: OperationDataRepairApprovalDecision
    actor: OperationDataRepairApprovalActor
    comment: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        step = self.step

        decision: str = self.decision

        actor = self.actor.to_dict()

        comment = self.comment

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "step": step,
                "decision": decision,
                "actor": actor,
            }
        )
        if comment is not UNSET:
            field_dict["comment"] = comment

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_data_repair_approval_actor import OperationDataRepairApprovalActor

        d = dict(src_dict)
        step = d.pop("step")

        decision = check_operation_data_repair_approval_decision(d.pop("decision"))

        actor = OperationDataRepairApprovalActor.from_dict(d.pop("actor"))

        comment = d.pop("comment", UNSET)

        operation_data_repair_approval = cls(
            step=step,
            decision=decision,
            actor=actor,
            comment=comment,
        )

        operation_data_repair_approval.additional_properties = d
        return operation_data_repair_approval

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
