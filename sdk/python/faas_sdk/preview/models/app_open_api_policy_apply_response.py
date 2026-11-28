from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.edge_rule_response import EdgeRuleResponse
    from ..models.edge_rule_suggestion import EdgeRuleSuggestion


T = TypeVar("T", bound="AppOpenAPIPolicyApplyResponse")


@_attrs_define
class AppOpenAPIPolicyApplyResponse:
    """Deterministic OpenAPI policy plan or the result of an explicit apply.
    Planned=true means no mutation occurred. AppliedCount is zero for an
    idempotent no-op.

    """

    app_id: UUID
    match_host: str
    preview_sha256: str
    suggestions: list[EdgeRuleSuggestion]
    planned: bool
    applied: list[EdgeRuleResponse]
    applied_count: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        match_host = self.match_host

        preview_sha256 = self.preview_sha256

        suggestions = []
        for suggestions_item_data in self.suggestions:
            suggestions_item = suggestions_item_data.to_dict()
            suggestions.append(suggestions_item)

        planned = self.planned

        applied = []
        for applied_item_data in self.applied:
            applied_item = applied_item_data.to_dict()
            applied.append(applied_item)

        applied_count = self.applied_count

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "match_host": match_host,
                "preview_sha256": preview_sha256,
                "suggestions": suggestions,
                "planned": planned,
                "applied": applied,
                "applied_count": applied_count,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.edge_rule_response import EdgeRuleResponse
        from ..models.edge_rule_suggestion import EdgeRuleSuggestion

        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        match_host = d.pop("match_host")

        preview_sha256 = d.pop("preview_sha256")

        suggestions = []
        _suggestions = d.pop("suggestions")
        for suggestions_item_data in _suggestions:
            suggestions_item = EdgeRuleSuggestion.from_dict(suggestions_item_data)

            suggestions.append(suggestions_item)

        planned = d.pop("planned")

        applied = []
        _applied = d.pop("applied")
        for applied_item_data in _applied:
            applied_item = EdgeRuleResponse.from_dict(applied_item_data)

            applied.append(applied_item)

        applied_count = d.pop("applied_count")

        app_open_api_policy_apply_response = cls(
            app_id=app_id,
            match_host=match_host,
            preview_sha256=preview_sha256,
            suggestions=suggestions,
            planned=planned,
            applied=applied,
            applied_count=applied_count,
        )

        app_open_api_policy_apply_response.additional_properties = d
        return app_open_api_policy_apply_response

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
