from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.route_advice_response_cacheability_coverage import (
    RouteAdviceResponseCacheabilityCoverage,
    check_route_advice_response_cacheability_coverage,
)
from ..models.route_advice_response_coverage import RouteAdviceResponseCoverage, check_route_advice_response_coverage
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.route_advice_policy_context import RouteAdvicePolicyContext
    from ..models.route_advice_suggestion import RouteAdviceSuggestion


T = TypeVar("T", bound="RouteAdviceResponse")


@_attrs_define
class RouteAdviceResponse:
    """Route advisor suggestions derived from retained request telemetry (ADR-976). Nothing is applied."""

    slug: str
    from_: datetime.datetime
    until: datetime.datetime
    cache_max_age_seconds: int
    routes_analyzed: int
    suggestions: list[RouteAdviceSuggestion]
    policy_context: RouteAdvicePolicyContext | Unset = UNSET
    """Bounded observation of selected-app desired cache, async and throttle rules matching the selected host, plus
    app-wide rules. Omits action and condition values; not proof of gateway convergence or authority to write."""
    app_id: str | Unset = UNSET
    """Advice report: App identity for explicitly scoped reports."""
    scope: str | Unset = UNSET
    """Advice report: Selected default or named environment; absent on aggregate reports."""
    project_id: str | Unset = UNSET
    """Advice report: Immutable project identity for named reports."""
    environment_id: str | Unset = UNSET
    """Advice report: Immutable named environment identity."""
    coverage: RouteAdviceResponseCoverage | Unset = UNSET
    """Advice report: Only retained, identity-matched deployment history is included; absent for aggregate reports."""
    partial: bool | Unset = UNSET
    """Advice report: True for scoped reports: unattributed history and telemetry gaps remain unknown."""
    cacheability_coverage: RouteAdviceResponseCacheabilityCoverage | Unset = UNSET
    """Scoped learned evidence excludes legacy comparisons that may cross environments."""
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

        policy_context: dict[str, Any] | Unset = UNSET
        if not isinstance(self.policy_context, Unset):
            policy_context = self.policy_context.to_dict()

        app_id = self.app_id

        scope = self.scope

        project_id = self.project_id

        environment_id = self.environment_id

        coverage: str | Unset = UNSET
        if not isinstance(self.coverage, Unset):
            coverage = self.coverage

        partial = self.partial

        cacheability_coverage: str | Unset = UNSET
        if not isinstance(self.cacheability_coverage, Unset):
            cacheability_coverage = self.cacheability_coverage

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
        if policy_context is not UNSET:
            field_dict["policy_context"] = policy_context
        if app_id is not UNSET:
            field_dict["app_id"] = app_id
        if scope is not UNSET:
            field_dict["scope"] = scope
        if project_id is not UNSET:
            field_dict["project_id"] = project_id
        if environment_id is not UNSET:
            field_dict["environment_id"] = environment_id
        if coverage is not UNSET:
            field_dict["coverage"] = coverage
        if partial is not UNSET:
            field_dict["partial"] = partial
        if cacheability_coverage is not UNSET:
            field_dict["cacheability_coverage"] = cacheability_coverage
        if window_clamped is not UNSET:
            field_dict["window_clamped"] = window_clamped

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_advice_policy_context import RouteAdvicePolicyContext
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

        _policy_context = d.pop("policy_context", UNSET)
        policy_context: RouteAdvicePolicyContext | Unset
        if isinstance(_policy_context, Unset):
            policy_context = UNSET
        else:
            policy_context = RouteAdvicePolicyContext.from_dict(_policy_context)

        app_id = d.pop("app_id", UNSET)

        scope = d.pop("scope", UNSET)

        project_id = d.pop("project_id", UNSET)

        environment_id = d.pop("environment_id", UNSET)

        _coverage = d.pop("coverage", UNSET)
        coverage: RouteAdviceResponseCoverage | Unset
        if isinstance(_coverage, Unset):
            coverage = UNSET
        else:
            coverage = check_route_advice_response_coverage(_coverage)

        partial = d.pop("partial", UNSET)

        _cacheability_coverage = d.pop("cacheability_coverage", UNSET)
        cacheability_coverage: RouteAdviceResponseCacheabilityCoverage | Unset
        if isinstance(_cacheability_coverage, Unset):
            cacheability_coverage = UNSET
        else:
            cacheability_coverage = check_route_advice_response_cacheability_coverage(_cacheability_coverage)

        window_clamped = d.pop("window_clamped", UNSET)

        route_advice_response = cls(
            slug=slug,
            from_=from_,
            until=until,
            cache_max_age_seconds=cache_max_age_seconds,
            routes_analyzed=routes_analyzed,
            suggestions=suggestions,
            policy_context=policy_context,
            app_id=app_id,
            scope=scope,
            project_id=project_id,
            environment_id=environment_id,
            coverage=coverage,
            partial=partial,
            cacheability_coverage=cacheability_coverage,
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
