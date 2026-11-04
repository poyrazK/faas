from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.api_consumer_plan_assignment_response import APIConsumerPlanAssignmentResponse


T = TypeVar("T", bound="APIConsumerPlanAssignmentListResponse")


@_attrs_define
class APIConsumerPlanAssignmentListResponse:
    """A consumer's plan assignments, oldest first."""

    assignments: list[APIConsumerPlanAssignmentResponse]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        assignments = []
        for assignments_item_data in self.assignments:
            assignments_item = assignments_item_data.to_dict()
            assignments.append(assignments_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "assignments": assignments,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.api_consumer_plan_assignment_response import APIConsumerPlanAssignmentResponse

        d = dict(src_dict)
        assignments = []
        _assignments = d.pop("assignments")
        for assignments_item_data in _assignments:
            assignments_item = APIConsumerPlanAssignmentResponse.from_dict(assignments_item_data)

            assignments.append(assignments_item)

        api_consumer_plan_assignment_list_response = cls(
            assignments=assignments,
        )

        api_consumer_plan_assignment_list_response.additional_properties = d
        return api_consumer_plan_assignment_list_response

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
