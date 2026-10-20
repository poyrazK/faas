from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.edge_rule_suggestion_kind import EdgeRuleSuggestionKind, check_edge_rule_suggestion_kind
from ..models.edge_rule_suggestion_methods_item import (
    EdgeRuleSuggestionMethodsItem,
    check_edge_rule_suggestion_methods_item,
)

if TYPE_CHECKING:
    from ..models.edge_rule_suggestion_action import EdgeRuleSuggestionAction


T = TypeVar("T", bound="EdgeRuleSuggestion")


@_attrs_define
class EdgeRuleSuggestion:
    """Single read-only candidate row in the dry-run response
    (issue #975 item #2 D3 / ADR-126). Mirrors the
    create-edge-rule request body fields so the customer can
    copy-paste the suggestion into the existing endpoint.
    `kind` + `action` union shape matches the existing
    `EdgeRule*Action` types in `pkg/api/dto.go`.

    """

    path: str
    """Operation path (e.g. `/users/{id}`)."""
    methods: list[EdgeRuleSuggestionMethodsItem]
    """HTTP methods the suggestion applies to."""
    kind: EdgeRuleSuggestionKind
    """Edge-rule kind the suggestion produces."""
    action: EdgeRuleSuggestionAction
    """Action payload (matches EdgeRule*Action types)."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        path = self.path

        methods = []
        for methods_item_data in self.methods:
            methods_item: str = methods_item_data
            methods.append(methods_item)

        kind: str = self.kind

        action = self.action.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "path": path,
                "methods": methods,
                "kind": kind,
                "action": action,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.edge_rule_suggestion_action import EdgeRuleSuggestionAction

        d = dict(src_dict)
        path = d.pop("path")

        methods = []
        _methods = d.pop("methods")
        for methods_item_data in _methods:
            methods_item = check_edge_rule_suggestion_methods_item(methods_item_data)

            methods.append(methods_item)

        kind = check_edge_rule_suggestion_kind(d.pop("kind"))

        action = EdgeRuleSuggestionAction.from_dict(d.pop("action"))

        edge_rule_suggestion = cls(
            path=path,
            methods=methods,
            kind=kind,
            action=action,
        )

        edge_rule_suggestion.additional_properties = d
        return edge_rule_suggestion

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
