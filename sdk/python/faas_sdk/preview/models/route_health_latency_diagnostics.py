from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.route_health_latency_diagnostics_coverage import (
    RouteHealthLatencyDiagnosticsCoverage,
    check_route_health_latency_diagnostics_coverage,
)

if TYPE_CHECKING:
    from ..models.route_health_dependency_comparison import RouteHealthDependencyComparison
    from ..models.route_health_latency_sample import RouteHealthLatencySample


T = TypeVar("T", bound="RouteHealthLatencyDiagnostics")


@_attrs_define
class RouteHealthLatencyDiagnostics:
    """Newest 32 retained rows per deployment/window, including rows without spans. Normalized type/kind groups exclude
    names, SQL and attributes. Sample percentiles are not additive and do not establish cause or complete capture.

    """

    coverage: RouteHealthLatencyDiagnosticsCoverage
    rows_limit: int
    dependencies_truncated: bool
    candidate: RouteHealthLatencySample
    """Retained row counts and publisher weights. Span samples are actual retained spans, at most 100 per row.
    Guest p95 weights measured rows, including zero. Wake boot p95 counts each distinct wake once and requires
    scheduler events scoped to the app and recorded instance on the selected deployment. Missing stage evidence is
    omitted; incomplete timing suppresses exclusive percentiles."""
    stable: RouteHealthLatencySample
    """Retained row counts and publisher weights. Span samples are actual retained spans, at most 100 per row.
    Guest p95 weights measured rows, including zero. Wake boot p95 counts each distinct wake once and requires
    scheduler events scoped to the app and recorded instance on the selected deployment. Missing stage evidence is
    omitted; incomplete timing suppresses exclusive percentiles."""
    dependencies: list[RouteHealthDependencyComparison]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        coverage: str = self.coverage

        rows_limit = self.rows_limit

        dependencies_truncated = self.dependencies_truncated

        candidate = self.candidate.to_dict()

        stable = self.stable.to_dict()

        dependencies = []
        for dependencies_item_data in self.dependencies:
            dependencies_item = dependencies_item_data.to_dict()
            dependencies.append(dependencies_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "coverage": coverage,
                "rows_limit": rows_limit,
                "dependencies_truncated": dependencies_truncated,
                "candidate": candidate,
                "stable": stable,
                "dependencies": dependencies,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_health_dependency_comparison import RouteHealthDependencyComparison
        from ..models.route_health_latency_sample import RouteHealthLatencySample

        d = dict(src_dict)
        coverage = check_route_health_latency_diagnostics_coverage(d.pop("coverage"))

        rows_limit = d.pop("rows_limit")

        dependencies_truncated = d.pop("dependencies_truncated")

        candidate = RouteHealthLatencySample.from_dict(d.pop("candidate"))

        stable = RouteHealthLatencySample.from_dict(d.pop("stable"))

        dependencies = []
        _dependencies = d.pop("dependencies")
        for dependencies_item_data in _dependencies:
            dependencies_item = RouteHealthDependencyComparison.from_dict(dependencies_item_data)

            dependencies.append(dependencies_item)

        route_health_latency_diagnostics = cls(
            coverage=coverage,
            rows_limit=rows_limit,
            dependencies_truncated=dependencies_truncated,
            candidate=candidate,
            stable=stable,
            dependencies=dependencies,
        )

        route_health_latency_diagnostics.additional_properties = d
        return route_health_latency_diagnostics

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
