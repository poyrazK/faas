from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.profile_attribution_quality import ProfileAttributionQuality
    from ..models.profile_coverage import ProfileCoverage
    from ..models.profile_function import ProfileFunction
    from ..models.profile_query import ProfileQuery
    from ..models.profile_route_cpu import ProfileRouteCPU
    from ..models.profile_source import ProfileSource
    from ..models.profile_stack import ProfileStack


T = TypeVar("T", bound="ProfileResponse")


@_attrs_define
class ProfileResponse:
    """CPU profile with an explicit absence-of-samples flag."""

    query: ProfileQuery
    """Authorized deployment CPU capture window."""
    cpu_seconds: float
    stack_count: int
    """Number of nonzero aggregated stack records, not raw sampling ticks."""
    functions: list[ProfileFunction]
    flamegraph: ProfileStack
    """One frame in a bounded CPU call-path tree."""
    empty: bool
    attribution: ProfileAttributionQuality | Unset = UNSET
    """Labeled and unattributed shares of all sampled CPU in the deployment window, even when a route filter is
    selected. Not VM CPU or request instrumentation completeness. Percentages are absent when no CPU was sampled.
   """
    routes: list[ProfileRouteCPU] | Unset = UNSET
    route_requests_complete: bool | Unset = UNSET
    """All observed request-route labels have CPU attribution and a complete bounded request summary. This does not
    establish telemetry delivery or instrumentation completeness."""
    coverage: ProfileCoverage | Unset = UNSET
    """Recorded collection evidence. Overlapping intervals count once; gaps can include idle time or loss. Failure
    counts are a lower bound; losses before ingestion are unknown. Unavailable coverage must not be interpreted as
    zero collection."""
    source: ProfileSource | Unset = UNSET
    """Deployment-recorded GitHub provenance. Links use a full immutable commit SHA; uploaded or generated source
    is not verified against that commit. Unavailable provenance is explicit."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        query = self.query.to_dict()

        cpu_seconds = self.cpu_seconds

        stack_count = self.stack_count

        functions = []
        for functions_item_data in self.functions:
            functions_item = functions_item_data.to_dict()
            functions.append(functions_item)

        flamegraph = self.flamegraph.to_dict()

        empty = self.empty

        attribution: dict[str, Any] | Unset = UNSET
        if not isinstance(self.attribution, Unset):
            attribution = self.attribution.to_dict()

        routes: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.routes, Unset):
            routes = []
            for routes_item_data in self.routes:
                routes_item = routes_item_data.to_dict()
                routes.append(routes_item)

        route_requests_complete = self.route_requests_complete

        coverage: dict[str, Any] | Unset = UNSET
        if not isinstance(self.coverage, Unset):
            coverage = self.coverage.to_dict()

        source: dict[str, Any] | Unset = UNSET
        if not isinstance(self.source, Unset):
            source = self.source.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "query": query,
                "cpu_seconds": cpu_seconds,
                "stack_count": stack_count,
                "functions": functions,
                "flamegraph": flamegraph,
                "empty": empty,
            }
        )
        if attribution is not UNSET:
            field_dict["attribution"] = attribution
        if routes is not UNSET:
            field_dict["routes"] = routes
        if route_requests_complete is not UNSET:
            field_dict["route_requests_complete"] = route_requests_complete
        if coverage is not UNSET:
            field_dict["coverage"] = coverage
        if source is not UNSET:
            field_dict["source"] = source

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.profile_attribution_quality import ProfileAttributionQuality
        from ..models.profile_coverage import ProfileCoverage
        from ..models.profile_function import ProfileFunction
        from ..models.profile_query import ProfileQuery
        from ..models.profile_route_cpu import ProfileRouteCPU
        from ..models.profile_source import ProfileSource
        from ..models.profile_stack import ProfileStack

        d = dict(src_dict)
        query = ProfileQuery.from_dict(d.pop("query"))

        cpu_seconds = d.pop("cpu_seconds")

        stack_count = d.pop("stack_count")

        functions = []
        _functions = d.pop("functions")
        for functions_item_data in _functions:
            functions_item = ProfileFunction.from_dict(functions_item_data)

            functions.append(functions_item)

        flamegraph = ProfileStack.from_dict(d.pop("flamegraph"))

        empty = d.pop("empty")

        _attribution = d.pop("attribution", UNSET)
        attribution: ProfileAttributionQuality | Unset
        if isinstance(_attribution, Unset):
            attribution = UNSET
        else:
            attribution = ProfileAttributionQuality.from_dict(_attribution)

        _routes = d.pop("routes", UNSET)
        routes: list[ProfileRouteCPU] | Unset = UNSET
        if _routes is not UNSET:
            routes = []
            for routes_item_data in _routes:
                routes_item = ProfileRouteCPU.from_dict(routes_item_data)

                routes.append(routes_item)

        route_requests_complete = d.pop("route_requests_complete", UNSET)

        _coverage = d.pop("coverage", UNSET)
        coverage: ProfileCoverage | Unset
        if isinstance(_coverage, Unset):
            coverage = UNSET
        else:
            coverage = ProfileCoverage.from_dict(_coverage)

        _source = d.pop("source", UNSET)
        source: ProfileSource | Unset
        if isinstance(_source, Unset):
            source = UNSET
        else:
            source = ProfileSource.from_dict(_source)

        profile_response = cls(
            query=query,
            cpu_seconds=cpu_seconds,
            stack_count=stack_count,
            functions=functions,
            flamegraph=flamegraph,
            empty=empty,
            attribution=attribution,
            routes=routes,
            route_requests_complete=route_requests_complete,
            coverage=coverage,
            source=source,
        )

        profile_response.additional_properties = d
        return profile_response

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
