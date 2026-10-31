from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.application_standard_publisher import ApplicationStandardPublisher


T = TypeVar("T", bound="ApplicationStandardPublisherList")


@_attrs_define
class ApplicationStandardPublisherList:
    """A page of approved publisher keys."""

    publishers: list[ApplicationStandardPublisher]
    next_page_after: UUID | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        publishers = []
        for publishers_item_data in self.publishers:
            publishers_item = publishers_item_data.to_dict()
            publishers.append(publishers_item)

        next_page_after: str | Unset = UNSET
        if not isinstance(self.next_page_after, Unset):
            next_page_after = str(self.next_page_after)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "publishers": publishers,
            }
        )
        if next_page_after is not UNSET:
            field_dict["next_page_after"] = next_page_after

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.application_standard_publisher import ApplicationStandardPublisher

        d = dict(src_dict)
        publishers = []
        _publishers = d.pop("publishers")
        for publishers_item_data in _publishers:
            publishers_item = ApplicationStandardPublisher.from_dict(publishers_item_data)

            publishers.append(publishers_item)

        _next_page_after = d.pop("next_page_after", UNSET)
        next_page_after: UUID | Unset
        if isinstance(_next_page_after, Unset):
            next_page_after = UNSET
        else:
            next_page_after = UUID(_next_page_after)

        application_standard_publisher_list = cls(
            publishers=publishers,
            next_page_after=next_page_after,
        )

        return application_standard_publisher_list
