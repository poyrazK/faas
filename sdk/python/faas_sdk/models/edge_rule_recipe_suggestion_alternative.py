from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.edge_rule_recipe_suggestion_alternative_params import EdgeRuleRecipeSuggestionAlternativeParams


T = TypeVar("T", bound="EdgeRuleRecipeSuggestionAlternative")


@_attrs_define
class EdgeRuleRecipeSuggestionAlternative:
    """A softer recipe for the same observation, such as challenging browsers instead of rate-limiting."""

    recipe: str
    """Recipe id of the alternative."""
    title: str
    """The alternative recipe's title."""
    params: EdgeRuleRecipeSuggestionAlternativeParams | Unset = UNSET
    """Parameters to add the alternative with."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        recipe = self.recipe

        title = self.title

        params: dict[str, Any] | Unset = UNSET
        if not isinstance(self.params, Unset):
            params = self.params.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "recipe": recipe,
                "title": title,
            }
        )
        if params is not UNSET:
            field_dict["params"] = params

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.edge_rule_recipe_suggestion_alternative_params import EdgeRuleRecipeSuggestionAlternativeParams

        d = dict(src_dict)
        recipe = d.pop("recipe")

        title = d.pop("title")

        _params = d.pop("params", UNSET)
        params: EdgeRuleRecipeSuggestionAlternativeParams | Unset
        if isinstance(_params, Unset):
            params = UNSET
        else:
            params = EdgeRuleRecipeSuggestionAlternativeParams.from_dict(_params)

        edge_rule_recipe_suggestion_alternative = cls(
            recipe=recipe,
            title=title,
            params=params,
        )

        edge_rule_recipe_suggestion_alternative.additional_properties = d
        return edge_rule_recipe_suggestion_alternative

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
