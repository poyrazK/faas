from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.profile_attribution_comparison import ProfileAttributionComparison
    from ..models.profile_function_delta import ProfileFunctionDelta
    from ..models.profile_response import ProfileResponse
    from ..models.profile_route_adjustment import ProfileRouteAdjustment
    from ..models.profile_stack_delta import ProfileStackDelta


T = TypeVar("T", bound="ProfileCompareResponse")


@_attrs_define
class ProfileCompareResponse:
    """Deployment CPU rate differences with a compatibility decision."""

    baseline: ProfileResponse
    """CPU profile with an explicit absence-of-samples flag."""
    candidate: ProfileResponse
    """CPU profile with an explicit absence-of-samples flag."""
    functions: list[ProfileFunctionDelta]
    comparable: bool
    attribution: ProfileAttributionComparison | Unset = UNSET
    """Captured route attribution quality comparison. An absolute labeled-share change of at least 20 percentage
    points suppresses advisory route regression conclusions without changing aggregate results or rollout behavior.
    Background work and traffic changes can also change labeled CPU share."""
    route_adjustment: ProfileRouteAdjustment | Unset = UNSET
    """Common request mix uses the mean of stable/canary route shares. Requires matching route sets, no
    unattributed CPU, sufficient collection coverage and at least 20 requests per route on each side. This does not
    alter canary outcomes."""
    reason: str | Unset = UNSET
    flamegraph: ProfileStackDelta | Unset = UNSET
    """Inclusive CPU rates for one complete caller path. Rates are normalized by each selected window; omitted
    rates mean the path was not observed, not measured zero. Delta is omitted unless both sides were observed. Width
    is the sum of observed rates for an additive union layout. Named frames match by symbol and file within their
    parent path; unknown and anonymous frames also retain their line identity."""
    flamegraph_reason: str | Unset = UNSET
    """Explains unavailable differential data while preserving a valid function comparison."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        baseline = self.baseline.to_dict()

        candidate = self.candidate.to_dict()

        functions = []
        for functions_item_data in self.functions:
            functions_item = functions_item_data.to_dict()
            functions.append(functions_item)

        comparable = self.comparable

        attribution: dict[str, Any] | Unset = UNSET
        if not isinstance(self.attribution, Unset):
            attribution = self.attribution.to_dict()

        route_adjustment: dict[str, Any] | Unset = UNSET
        if not isinstance(self.route_adjustment, Unset):
            route_adjustment = self.route_adjustment.to_dict()

        reason = self.reason

        flamegraph: dict[str, Any] | Unset = UNSET
        if not isinstance(self.flamegraph, Unset):
            flamegraph = self.flamegraph.to_dict()

        flamegraph_reason = self.flamegraph_reason

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "baseline": baseline,
                "candidate": candidate,
                "functions": functions,
                "comparable": comparable,
            }
        )
        if attribution is not UNSET:
            field_dict["attribution"] = attribution
        if route_adjustment is not UNSET:
            field_dict["route_adjustment"] = route_adjustment
        if reason is not UNSET:
            field_dict["reason"] = reason
        if flamegraph is not UNSET:
            field_dict["flamegraph"] = flamegraph
        if flamegraph_reason is not UNSET:
            field_dict["flamegraph_reason"] = flamegraph_reason

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.profile_attribution_comparison import ProfileAttributionComparison
        from ..models.profile_function_delta import ProfileFunctionDelta
        from ..models.profile_response import ProfileResponse
        from ..models.profile_route_adjustment import ProfileRouteAdjustment
        from ..models.profile_stack_delta import ProfileStackDelta

        d = dict(src_dict)
        baseline = ProfileResponse.from_dict(d.pop("baseline"))

        candidate = ProfileResponse.from_dict(d.pop("candidate"))

        functions = []
        _functions = d.pop("functions")
        for functions_item_data in _functions:
            functions_item = ProfileFunctionDelta.from_dict(functions_item_data)

            functions.append(functions_item)

        comparable = d.pop("comparable")

        _attribution = d.pop("attribution", UNSET)
        attribution: ProfileAttributionComparison | Unset
        if isinstance(_attribution, Unset):
            attribution = UNSET
        else:
            attribution = ProfileAttributionComparison.from_dict(_attribution)

        _route_adjustment = d.pop("route_adjustment", UNSET)
        route_adjustment: ProfileRouteAdjustment | Unset
        if isinstance(_route_adjustment, Unset):
            route_adjustment = UNSET
        else:
            route_adjustment = ProfileRouteAdjustment.from_dict(_route_adjustment)

        reason = d.pop("reason", UNSET)

        _flamegraph = d.pop("flamegraph", UNSET)
        flamegraph: ProfileStackDelta | Unset
        if isinstance(_flamegraph, Unset):
            flamegraph = UNSET
        else:
            flamegraph = ProfileStackDelta.from_dict(_flamegraph)

        flamegraph_reason = d.pop("flamegraph_reason", UNSET)

        profile_compare_response = cls(
            baseline=baseline,
            candidate=candidate,
            functions=functions,
            comparable=comparable,
            attribution=attribution,
            route_adjustment=route_adjustment,
            reason=reason,
            flamegraph=flamegraph,
            flamegraph_reason=flamegraph_reason,
        )

        profile_compare_response.additional_properties = d
        return profile_compare_response

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
