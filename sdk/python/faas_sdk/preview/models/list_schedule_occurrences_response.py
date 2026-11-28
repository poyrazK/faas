from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.schedule_occurrence_response import ScheduleOccurrenceResponse


T = TypeVar("T", bound="ListScheduleOccurrencesResponse")


@_attrs_define
class ListScheduleOccurrencesResponse:
    """Newest-first page of scheduled occurrence decisions."""

    occurrences: list[ScheduleOccurrenceResponse]
    limit: int
    before: UUID | Unset = UNSET
    next_before: UUID | Unset = UNSET
    """Pass this id as before to read an older page; omitted when the page is complete."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        occurrences = []
        for occurrences_item_data in self.occurrences:
            occurrences_item = occurrences_item_data.to_dict()
            occurrences.append(occurrences_item)

        limit = self.limit

        before: str | Unset = UNSET
        if not isinstance(self.before, Unset):
            before = str(self.before)

        next_before: str | Unset = UNSET
        if not isinstance(self.next_before, Unset):
            next_before = str(self.next_before)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "occurrences": occurrences,
                "limit": limit,
            }
        )
        if before is not UNSET:
            field_dict["before"] = before
        if next_before is not UNSET:
            field_dict["next_before"] = next_before

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.schedule_occurrence_response import ScheduleOccurrenceResponse

        d = dict(src_dict)
        occurrences = []
        _occurrences = d.pop("occurrences")
        for occurrences_item_data in _occurrences:
            occurrences_item = ScheduleOccurrenceResponse.from_dict(occurrences_item_data)

            occurrences.append(occurrences_item)

        limit = d.pop("limit")

        _before = d.pop("before", UNSET)
        before: UUID | Unset
        if isinstance(_before, Unset):
            before = UNSET
        else:
            before = UUID(_before)

        _next_before = d.pop("next_before", UNSET)
        next_before: UUID | Unset
        if isinstance(_next_before, Unset):
            next_before = UNSET
        else:
            next_before = UUID(_next_before)

        list_schedule_occurrences_response = cls(
            occurrences=occurrences,
            limit=limit,
            before=before,
            next_before=next_before,
        )

        list_schedule_occurrences_response.additional_properties = d
        return list_schedule_occurrences_response

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
