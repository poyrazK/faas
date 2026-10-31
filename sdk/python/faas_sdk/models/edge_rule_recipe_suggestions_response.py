from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.edge_rule_recipe_suggestion import EdgeRuleRecipeSuggestion


T = TypeVar("T", bound="EdgeRuleRecipeSuggestionsResponse")


@_attrs_define
class EdgeRuleRecipeSuggestionsResponse:
    """Protection recipes suggested from the app's traffic."""

    window: str
    """Telemetry window the suggestions read, in days: 7d, or the plan's telemetry retention when shorter."""
    since: datetime.datetime
    """Start of the telemetry window."""
    suggestions: list[EdgeRuleRecipeSuggestion]
    """Ranked suggestions, at most five."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        window = self.window

        since = self.since.isoformat()

        suggestions = []
        for suggestions_item_data in self.suggestions:
            suggestions_item = suggestions_item_data.to_dict()
            suggestions.append(suggestions_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "window": window,
                "since": since,
                "suggestions": suggestions,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.edge_rule_recipe_suggestion import EdgeRuleRecipeSuggestion

        d = dict(src_dict)
        window = d.pop("window")

        since = datetime.datetime.fromisoformat(d.pop("since"))

        suggestions = []
        _suggestions = d.pop("suggestions")
        for suggestions_item_data in _suggestions:
            suggestions_item = EdgeRuleRecipeSuggestion.from_dict(suggestions_item_data)

            suggestions.append(suggestions_item)

        edge_rule_recipe_suggestions_response = cls(
            window=window,
            since=since,
            suggestions=suggestions,
        )

        edge_rule_recipe_suggestions_response.additional_properties = d
        return edge_rule_recipe_suggestions_response

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
