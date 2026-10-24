from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.log_saved_search import LogSavedSearch


T = TypeVar("T", bound="LogSavedSearchesResponse")


@_attrs_define
class LogSavedSearchesResponse:
    """An app's saved log searches by name."""

    searches: list[LogSavedSearch]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        searches = []
        for searches_item_data in self.searches:
            searches_item = searches_item_data.to_dict()
            searches.append(searches_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "searches": searches,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.log_saved_search import LogSavedSearch

        d = dict(src_dict)
        searches = []
        _searches = d.pop("searches")
        for searches_item_data in _searches:
            searches_item = LogSavedSearch.from_dict(searches_item_data)

            searches.append(searches_item)

        log_saved_searches_response = cls(
            searches=searches,
        )

        log_saved_searches_response.additional_properties = d
        return log_saved_searches_response

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
