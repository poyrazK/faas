from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.route_awake_suggestion_kind import RouteAwakeSuggestionKind, check_route_awake_suggestion_kind

T = TypeVar("T", bound="RouteAwakeSuggestion")


@_attrs_define
class RouteAwakeSuggestion:
    """A change that would let the app park."""

    kind: RouteAwakeSuggestionKind
    message: str
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        kind: str = self.kind

        message = self.message

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "kind": kind,
                "message": message,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        kind = check_route_awake_suggestion_kind(d.pop("kind"))

        message = d.pop("message")

        route_awake_suggestion = cls(
            kind=kind,
            message=message,
        )

        route_awake_suggestion.additional_properties = d
        return route_awake_suggestion

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
