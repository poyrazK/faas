from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.app_open_api_policy_preview_route import AppOpenAPIPolicyPreviewRoute
    from ..models.edge_rule_suggestion import EdgeRuleSuggestion


T = TypeVar("T", bound="AppOpenAPIPolicyPreviewResponse")


@_attrs_define
class AppOpenAPIPolicyPreviewResponse:
    """Read-only declared-vs-observed route and edge-policy preview
    (API-hosting roadmap item 11 / ADR-126 follow-up). The response
    never writes an OpenAPI document or edge rule.

    """

    app_id: UUID
    source: str
    """preview, empty: no_import, or degraded: routes_unavailable."""
    observed_available: bool
    """Whether the gatewayd observed-route bridge returned successfully."""
    routes: list[AppOpenAPIPolicyPreviewRoute]
    openapi_version: str | Unset = UNSET
    """OpenAPI version from the persisted declaration, when present."""
    suggestions: list[EdgeRuleSuggestion] | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        source = self.source

        observed_available = self.observed_available

        routes = []
        for routes_item_data in self.routes:
            routes_item = routes_item_data.to_dict()
            routes.append(routes_item)

        openapi_version = self.openapi_version

        suggestions: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.suggestions, Unset):
            suggestions = []
            for suggestions_item_data in self.suggestions:
                suggestions_item = suggestions_item_data.to_dict()
                suggestions.append(suggestions_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "source": source,
                "observed_available": observed_available,
                "routes": routes,
            }
        )
        if openapi_version is not UNSET:
            field_dict["openapi_version"] = openapi_version
        if suggestions is not UNSET:
            field_dict["suggestions"] = suggestions

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.app_open_api_policy_preview_route import AppOpenAPIPolicyPreviewRoute
        from ..models.edge_rule_suggestion import EdgeRuleSuggestion

        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        source = d.pop("source")

        observed_available = d.pop("observed_available")

        routes = []
        _routes = d.pop("routes")
        for routes_item_data in _routes:
            routes_item = AppOpenAPIPolicyPreviewRoute.from_dict(routes_item_data)

            routes.append(routes_item)

        openapi_version = d.pop("openapi_version", UNSET)

        _suggestions = d.pop("suggestions", UNSET)
        suggestions: list[EdgeRuleSuggestion] | Unset = UNSET
        if _suggestions is not UNSET:
            suggestions = []
            for suggestions_item_data in _suggestions:
                suggestions_item = EdgeRuleSuggestion.from_dict(suggestions_item_data)

                suggestions.append(suggestions_item)

        app_open_api_policy_preview_response = cls(
            app_id=app_id,
            source=source,
            observed_available=observed_available,
            routes=routes,
            openapi_version=openapi_version,
            suggestions=suggestions,
        )

        app_open_api_policy_preview_response.additional_properties = d
        return app_open_api_policy_preview_response

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
