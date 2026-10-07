from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.operation_milestone_request import OperationMilestoneRequest


T = TypeVar("T", bound="OperationMilestoneValidationRequest")


@_attrs_define
class OperationMilestoneValidationRequest:
    """Bounded read-only precommit validation; does not reserve capacity, extend the claim, or publish facts. Maximum
    request size 65536 bytes.

    """

    milestones: list[OperationMilestoneRequest]

    def to_dict(self) -> dict[str, Any]:
        milestones = []
        for milestones_item_data in self.milestones:
            milestones_item = milestones_item_data.to_dict()
            milestones.append(milestones_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "milestones": milestones,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_milestone_request import OperationMilestoneRequest

        d = dict(src_dict)
        milestones = []
        _milestones = d.pop("milestones")
        for milestones_item_data in _milestones:
            milestones_item = OperationMilestoneRequest.from_dict(milestones_item_data)

            milestones.append(milestones_item)

        operation_milestone_validation_request = cls(
            milestones=milestones,
        )

        return operation_milestone_validation_request
