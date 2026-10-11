from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.profile_route_regression_status import ProfileRouteRegressionStatus, check_profile_route_regression_status
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.profile_regression_cpu_per_request_metric import ProfileRegressionCPUPerRequestMetric
    from ..models.profile_regression_evidence import ProfileRegressionEvidence
    from ..models.profile_route_label_comparison import ProfileRouteLabelComparison


T = TypeVar("T", bound="ProfileRouteRegression")


@_attrs_define
class ProfileRouteRegression:
    """Retained advisory route-associated sampled CPU/request observation. Missing attribution, sparse requests or
    inadequate capture coverage produces insufficient_data. No rollout decision is changed.

    """

    route: str
    status: ProfileRouteRegressionStatus
    reason: str
    code_reason: str | Unset = UNSET
    """Availability and limitations of route-filtered code attribution."""
    code_evidence: list[ProfileRegressionEvidence] | Unset = UNSET
    """Bounded route-filtered function self CPU and inclusive call-path increases ranked by CPU/request delta.
    Source URLs are derived at read time."""
    label_coverage: ProfileRouteLabelComparison | Unset = UNSET
    """Advisory route CPU/request requires reconciled labeling shares of at least 80 percent in both windows and a
    change below 20 percentage points in either direction. Missing reports or failed reconciliation produces
    insufficient data."""
    baseline_requests: int | Unset = UNSET
    candidate_requests: int | Unset = UNSET
    metric: ProfileRegressionCPUPerRequestMetric | Unset = UNSET
    """Qualified route CPU/request measurements and regression threshold results."""
    comparison_url: str | Unset = UNSET
    """Request-time dashboard link to the route differential flamegraph for the frozen comparison windows."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        route = self.route

        status: str = self.status

        reason = self.reason

        code_reason = self.code_reason

        code_evidence: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.code_evidence, Unset):
            code_evidence = []
            for code_evidence_item_data in self.code_evidence:
                code_evidence_item = code_evidence_item_data.to_dict()
                code_evidence.append(code_evidence_item)

        label_coverage: dict[str, Any] | Unset = UNSET
        if not isinstance(self.label_coverage, Unset):
            label_coverage = self.label_coverage.to_dict()

        baseline_requests = self.baseline_requests

        candidate_requests = self.candidate_requests

        metric: dict[str, Any] | Unset = UNSET
        if not isinstance(self.metric, Unset):
            metric = self.metric.to_dict()

        comparison_url = self.comparison_url

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "route": route,
                "status": status,
                "reason": reason,
            }
        )
        if code_reason is not UNSET:
            field_dict["code_reason"] = code_reason
        if code_evidence is not UNSET:
            field_dict["code_evidence"] = code_evidence
        if label_coverage is not UNSET:
            field_dict["label_coverage"] = label_coverage
        if baseline_requests is not UNSET:
            field_dict["baseline_requests"] = baseline_requests
        if candidate_requests is not UNSET:
            field_dict["candidate_requests"] = candidate_requests
        if metric is not UNSET:
            field_dict["metric"] = metric
        if comparison_url is not UNSET:
            field_dict["comparison_url"] = comparison_url

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.profile_regression_cpu_per_request_metric import ProfileRegressionCPUPerRequestMetric
        from ..models.profile_regression_evidence import ProfileRegressionEvidence
        from ..models.profile_route_label_comparison import ProfileRouteLabelComparison

        d = dict(src_dict)
        route = d.pop("route")

        status = check_profile_route_regression_status(d.pop("status"))

        reason = d.pop("reason")

        code_reason = d.pop("code_reason", UNSET)

        _code_evidence = d.pop("code_evidence", UNSET)
        code_evidence: list[ProfileRegressionEvidence] | Unset = UNSET
        if _code_evidence is not UNSET:
            code_evidence = []
            for code_evidence_item_data in _code_evidence:
                code_evidence_item = ProfileRegressionEvidence.from_dict(code_evidence_item_data)

                code_evidence.append(code_evidence_item)

        _label_coverage = d.pop("label_coverage", UNSET)
        label_coverage: ProfileRouteLabelComparison | Unset
        if isinstance(_label_coverage, Unset):
            label_coverage = UNSET
        else:
            label_coverage = ProfileRouteLabelComparison.from_dict(_label_coverage)

        baseline_requests = d.pop("baseline_requests", UNSET)

        candidate_requests = d.pop("candidate_requests", UNSET)

        _metric = d.pop("metric", UNSET)
        metric: ProfileRegressionCPUPerRequestMetric | Unset
        if isinstance(_metric, Unset):
            metric = UNSET
        else:
            metric = ProfileRegressionCPUPerRequestMetric.from_dict(_metric)

        comparison_url = d.pop("comparison_url", UNSET)

        profile_route_regression = cls(
            route=route,
            status=status,
            reason=reason,
            code_reason=code_reason,
            code_evidence=code_evidence,
            label_coverage=label_coverage,
            baseline_requests=baseline_requests,
            candidate_requests=candidate_requests,
            metric=metric,
            comparison_url=comparison_url,
        )

        profile_route_regression.additional_properties = d
        return profile_route_regression

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
