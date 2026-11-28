from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.edge_rule_recipe_suggestion_alternative import EdgeRuleRecipeSuggestionAlternative
    from ..models.edge_rule_recipe_suggestion_params import EdgeRuleRecipeSuggestionParams


T = TypeVar("T", bound="EdgeRuleRecipeSuggestion")


@_attrs_define
class EdgeRuleRecipeSuggestion:
    """One suggested protection recipe and why."""

    recipe: str
    """Recipe id to add."""
    title: str
    """The recipe's title."""
    reason: str
    """What in the app's traffic led to the suggestion."""
    params: EdgeRuleRecipeSuggestionParams | Unset = UNSET
    """Recipe parameters to add it with; required parameters not listed must be supplied."""
    alternative: EdgeRuleRecipeSuggestionAlternative | Unset = UNSET
    """A softer recipe for the same observation, such as challenging browsers instead of rate-limiting."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        recipe = self.recipe

        title = self.title

        reason = self.reason

        params: dict[str, Any] | Unset = UNSET
        if not isinstance(self.params, Unset):
            params = self.params.to_dict()

        alternative: dict[str, Any] | Unset = UNSET
        if not isinstance(self.alternative, Unset):
            alternative = self.alternative.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "recipe": recipe,
                "title": title,
                "reason": reason,
            }
        )
        if params is not UNSET:
            field_dict["params"] = params
        if alternative is not UNSET:
            field_dict["alternative"] = alternative

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.edge_rule_recipe_suggestion_alternative import EdgeRuleRecipeSuggestionAlternative
        from ..models.edge_rule_recipe_suggestion_params import EdgeRuleRecipeSuggestionParams

        d = dict(src_dict)
        recipe = d.pop("recipe")

        title = d.pop("title")

        reason = d.pop("reason")

        _params = d.pop("params", UNSET)
        params: EdgeRuleRecipeSuggestionParams | Unset
        if isinstance(_params, Unset):
            params = UNSET
        else:
            params = EdgeRuleRecipeSuggestionParams.from_dict(_params)

        _alternative = d.pop("alternative", UNSET)
        alternative: EdgeRuleRecipeSuggestionAlternative | Unset
        if isinstance(_alternative, Unset):
            alternative = UNSET
        else:
            alternative = EdgeRuleRecipeSuggestionAlternative.from_dict(_alternative)

        edge_rule_recipe_suggestion = cls(
            recipe=recipe,
            title=title,
            reason=reason,
            params=params,
            alternative=alternative,
        )

        edge_rule_recipe_suggestion.additional_properties = d
        return edge_rule_recipe_suggestion

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
