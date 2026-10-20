from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.edge_rule_suggestion import EdgeRuleSuggestion


T = TypeVar("T", bound="AppOpenAPIImportDryRunResponse")


@_attrs_define
class AppOpenAPIImportDryRunResponse:
    """Response body for `POST /v1/apps/{slug}/openapi/dry-run`
    (issue #975 item #2 D3 / ADR-126). Read-only; no persist,
    no `pg_notify`, no MFA. Empty `suggestions` array when the
    doc is fully covered by existing validate edge rules.

    """

    suggestions: list[EdgeRuleSuggestion]
    """Suggested EdgeRuleSuggestion rows."""
    openapi_version: str
    """OpenAPI version declared by the dry-run doc."""
    endpoint_count: int
    """Number of operations in the dry-run doc."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        suggestions = []
        for suggestions_item_data in self.suggestions:
            suggestions_item = suggestions_item_data.to_dict()
            suggestions.append(suggestions_item)

        openapi_version = self.openapi_version

        endpoint_count = self.endpoint_count

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "suggestions": suggestions,
                "openapi_version": openapi_version,
                "endpoint_count": endpoint_count,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.edge_rule_suggestion import EdgeRuleSuggestion

        d = dict(src_dict)
        suggestions = []
        _suggestions = d.pop("suggestions")
        for suggestions_item_data in _suggestions:
            suggestions_item = EdgeRuleSuggestion.from_dict(suggestions_item_data)

            suggestions.append(suggestions_item)

        openapi_version = d.pop("openapi_version")

        endpoint_count = d.pop("endpoint_count")

        app_open_api_import_dry_run_response = cls(
            suggestions=suggestions,
            openapi_version=openapi_version,
            endpoint_count=endpoint_count,
        )

        app_open_api_import_dry_run_response.additional_properties = d
        return app_open_api_import_dry_run_response

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
