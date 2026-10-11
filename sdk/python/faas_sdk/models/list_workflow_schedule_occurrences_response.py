from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.workflow_schedule_occurrence_response import WorkflowScheduleOccurrenceResponse


T = TypeVar("T", bound="ListWorkflowScheduleOccurrencesResponse")


@_attrs_define
class ListWorkflowScheduleOccurrencesResponse:
    """Page of due workflow schedule outcomes ordered by nominal minute, with a cursor for the next page."""

    occurrences: list[WorkflowScheduleOccurrenceResponse]
    next_cursor: UUID | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        occurrences = []
        for occurrences_item_data in self.occurrences:
            occurrences_item = occurrences_item_data.to_dict()
            occurrences.append(occurrences_item)

        next_cursor: str | Unset = UNSET
        if not isinstance(self.next_cursor, Unset):
            next_cursor = str(self.next_cursor)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "occurrences": occurrences,
            }
        )
        if next_cursor is not UNSET:
            field_dict["next_cursor"] = next_cursor

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.workflow_schedule_occurrence_response import WorkflowScheduleOccurrenceResponse

        d = dict(src_dict)
        occurrences = []
        _occurrences = d.pop("occurrences")
        for occurrences_item_data in _occurrences:
            occurrences_item = WorkflowScheduleOccurrenceResponse.from_dict(occurrences_item_data)

            occurrences.append(occurrences_item)

        _next_cursor = d.pop("next_cursor", UNSET)
        next_cursor: UUID | Unset
        if isinstance(_next_cursor, Unset):
            next_cursor = UNSET
        else:
            next_cursor = UUID(_next_cursor)

        list_workflow_schedule_occurrences_response = cls(
            occurrences=occurrences,
            next_cursor=next_cursor,
        )

        list_workflow_schedule_occurrences_response.additional_properties = d
        return list_workflow_schedule_occurrences_response

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
