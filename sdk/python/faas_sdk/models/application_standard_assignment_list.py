from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.application_standard_assignment import ApplicationStandardAssignment


T = TypeVar("T", bound="ApplicationStandardAssignmentList")


@_attrs_define
class ApplicationStandardAssignmentList:
    assignments: list[ApplicationStandardAssignment]
    next_page_after: UUID | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        assignments = []
        for assignments_item_data in self.assignments:
            assignments_item = assignments_item_data.to_dict()
            assignments.append(assignments_item)

        next_page_after: str | Unset = UNSET
        if not isinstance(self.next_page_after, Unset):
            next_page_after = str(self.next_page_after)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "assignments": assignments,
            }
        )
        if next_page_after is not UNSET:
            field_dict["next_page_after"] = next_page_after

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.application_standard_assignment import ApplicationStandardAssignment

        d = dict(src_dict)
        assignments = []
        _assignments = d.pop("assignments")
        for assignments_item_data in _assignments:
            assignments_item = ApplicationStandardAssignment.from_dict(assignments_item_data)

            assignments.append(assignments_item)

        _next_page_after = d.pop("next_page_after", UNSET)
        next_page_after: UUID | Unset
        if isinstance(_next_page_after, Unset):
            next_page_after = UNSET
        else:
            next_page_after = UUID(_next_page_after)

        application_standard_assignment_list = cls(
            assignments=assignments,
            next_page_after=next_page_after,
        )

        application_standard_assignment_list.additional_properties = d
        return application_standard_assignment_list

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
