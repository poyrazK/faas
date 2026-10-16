from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.application_standard_log_destination import ApplicationStandardLogDestination


T = TypeVar("T", bound="ApplicationStandardLogDestinationList")


@_attrs_define
class ApplicationStandardLogDestinationList:
    """A page of immutable logging destinations."""

    destinations: list[ApplicationStandardLogDestination]
    next_page_after: UUID | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        destinations = []
        for destinations_item_data in self.destinations:
            destinations_item = destinations_item_data.to_dict()
            destinations.append(destinations_item)

        next_page_after: str | Unset = UNSET
        if not isinstance(self.next_page_after, Unset):
            next_page_after = str(self.next_page_after)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "destinations": destinations,
            }
        )
        if next_page_after is not UNSET:
            field_dict["next_page_after"] = next_page_after

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.application_standard_log_destination import ApplicationStandardLogDestination

        d = dict(src_dict)
        destinations = []
        _destinations = d.pop("destinations")
        for destinations_item_data in _destinations:
            destinations_item = ApplicationStandardLogDestination.from_dict(destinations_item_data)

            destinations.append(destinations_item)

        _next_page_after = d.pop("next_page_after", UNSET)
        next_page_after: UUID | Unset
        if isinstance(_next_page_after, Unset):
            next_page_after = UNSET
        else:
            next_page_after = UUID(_next_page_after)

        application_standard_log_destination_list = cls(
            destinations=destinations,
            next_page_after=next_page_after,
        )

        return application_standard_log_destination_list
