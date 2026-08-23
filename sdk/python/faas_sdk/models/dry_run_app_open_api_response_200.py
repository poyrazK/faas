from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.dry_run_app_open_api_response_200_suggestions_item import DryRunAppOpenAPIResponse200SuggestionsItem


T = TypeVar("T", bound="DryRunAppOpenAPIResponse200")


@_attrs_define
class DryRunAppOpenAPIResponse200:
    app_id: UUID
    openapi_version: str
    endpoint_count: int
    suggestions: list[DryRunAppOpenAPIResponse200SuggestionsItem]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        openapi_version = self.openapi_version

        endpoint_count = self.endpoint_count

        suggestions = []
        for suggestions_item_data in self.suggestions:
            suggestions_item = suggestions_item_data.to_dict()
            suggestions.append(suggestions_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "openapi_version": openapi_version,
                "endpoint_count": endpoint_count,
                "suggestions": suggestions,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.dry_run_app_open_api_response_200_suggestions_item import (
            DryRunAppOpenAPIResponse200SuggestionsItem,
        )

        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        openapi_version = d.pop("openapi_version")

        endpoint_count = d.pop("endpoint_count")

        suggestions = []
        _suggestions = d.pop("suggestions")
        for suggestions_item_data in _suggestions:
            suggestions_item = DryRunAppOpenAPIResponse200SuggestionsItem.from_dict(suggestions_item_data)

            suggestions.append(suggestions_item)

        dry_run_app_open_api_response_200 = cls(
            app_id=app_id,
            openapi_version=openapi_version,
            endpoint_count=endpoint_count,
            suggestions=suggestions,
        )

        dry_run_app_open_api_response_200.additional_properties = d
        return dry_run_app_open_api_response_200

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
