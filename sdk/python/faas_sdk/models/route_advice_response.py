from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.route_advice_suggestion import RouteAdviceSuggestion


T = TypeVar("T", bound="RouteAdviceResponse")


@_attrs_define
class RouteAdviceResponse:
    """Route advisor suggestions derived from retained request telemetry (ADR-940). Nothing is applied."""

    slug: str
    from_: datetime.datetime
    until: datetime.datetime
    cache_max_age_seconds: int
    routes_analyzed: int
    suggestions: list[RouteAdviceSuggestion]
    window_clamped: bool | Unset = UNSET
    """The requested window was shortened to plan retention."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        slug = self.slug

        from_ = self.from_.isoformat()

        until = self.until.isoformat()

        cache_max_age_seconds = self.cache_max_age_seconds

        routes_analyzed = self.routes_analyzed

        suggestions = []
        for suggestions_item_data in self.suggestions:
            suggestions_item = suggestions_item_data.to_dict()
            suggestions.append(suggestions_item)

        window_clamped = self.window_clamped

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "slug": slug,
                "from": from_,
                "until": until,
                "cache_max_age_seconds": cache_max_age_seconds,
                "routes_analyzed": routes_analyzed,
                "suggestions": suggestions,
            }
        )
        if window_clamped is not UNSET:
            field_dict["window_clamped"] = window_clamped

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_advice_suggestion import RouteAdviceSuggestion

        d = dict(src_dict)
        slug = d.pop("slug")

        from_ = datetime.datetime.fromisoformat(d.pop("from"))

        until = datetime.datetime.fromisoformat(d.pop("until"))

        cache_max_age_seconds = d.pop("cache_max_age_seconds")

        routes_analyzed = d.pop("routes_analyzed")

        suggestions = []
        _suggestions = d.pop("suggestions")
        for suggestions_item_data in _suggestions:
            suggestions_item = RouteAdviceSuggestion.from_dict(suggestions_item_data)

            suggestions.append(suggestions_item)

        window_clamped = d.pop("window_clamped", UNSET)

        route_advice_response = cls(
            slug=slug,
            from_=from_,
            until=until,
            cache_max_age_seconds=cache_max_age_seconds,
            routes_analyzed=routes_analyzed,
            suggestions=suggestions,
            window_clamped=window_clamped,
        )

        route_advice_response.additional_properties = d
        return route_advice_response

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
