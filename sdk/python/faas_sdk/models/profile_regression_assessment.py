from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.profile_regression_assessment_status import (
    ProfileRegressionAssessmentStatus,
    check_profile_regression_assessment_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.profile_attribution_comparison import ProfileAttributionComparison
    from ..models.profile_coverage import ProfileCoverage
    from ..models.profile_query import ProfileQuery
    from ..models.profile_regression_evidence import ProfileRegressionEvidence
    from ..models.profile_regression_metric import ProfileRegressionMetric
    from ..models.profile_regression_options import ProfileRegressionOptions
    from ..models.profile_request_mix_snapshot import ProfileRequestMixSnapshot
    from ..models.profile_route_regression import ProfileRouteRegression


T = TypeVar("T", bound="ProfileRegressionAssessment")


@_attrs_define
class ProfileRegressionAssessment:
    """Latest bounded historical CPU assessment, at most 64 KiB JSON, including at most 32 KiB evidence. Original samples
    are not retained. A different current saved revision marks this assessment stale. Profile expiry leaves this
    historical summary readable, without extending backend retention.

    """

    investigation_revision: int
    checked_at: datetime.datetime
    status: ProfileRegressionAssessmentStatus
    reason: str
    options: ProfileRegressionOptions
    """Threshold policy for sampled CPU per wall-clock second or per weighted observed request. Both relative and
    selected-metric absolute increase thresholds must be met. CPU-per-request mode also requires retained request
    telemetry and its configured minimum request count in both deployment windows; absent or sparse counts are
    inconclusive. Request telemetry rows use minute-bucket timestamps, so counts near window boundaries can be
    approximate and may be incomplete. Capture requirements apply separately to each profile window."""
    baseline: ProfileQuery
    """Authorized deployment CPU capture window."""
    candidate: ProfileQuery
    """Authorized deployment CPU capture window."""
    evidence: list[ProfileRegressionEvidence]
    uncomparable_entries: int
    attribution: ProfileAttributionComparison | Unset = UNSET
    """Captured route attribution quality comparison. An absolute labeled-share change of at least 20 percentage
    points suppresses advisory route regression conclusions without changing aggregate results or rollout behavior.
    Background work and traffic changes can also change labeled CPU share."""
    route_checks: list[ProfileRouteRegression] | Unset = UNSET
    request_mix: ProfileRequestMixSnapshot | Unset = UNSET
    """Frozen observed telemetry summary, at most 16 KiB JSON. Completeness describes route/status aggregation, not
    telemetry delivery. Readable with the assessment after raw request telemetry expires."""
    baseline_requests: int | Unset = UNSET
    """Weighted observed request telemetry count for the baseline window."""
    candidate_requests: int | Unset = UNSET
    """Weighted observed request telemetry count for the candidate window."""
    baseline_coverage: ProfileCoverage | Unset = UNSET
    """Recorded collection evidence. Overlapping intervals count once; gaps can include idle time or loss. Failure
    counts are a lower bound; losses before ingestion are unknown. Unavailable coverage must not be interpreted as
    zero collection."""
    candidate_coverage: ProfileCoverage | Unset = UNSET
    """Recorded collection evidence. Overlapping intervals count once; gaps can include idle time or loss. Failure
    counts are a lower bound; losses before ingestion are unknown. Unavailable coverage must not be interpreted as
    zero collection."""
    total: ProfileRegressionMetric | Unset = UNSET
    """Observed sampled CPU/s comparison, optionally with CPU seconds per weighted observed request.
    exceeds_threshold and relative_increase_percent use the metric selected by options. The relative percentage is
    absent when that metric's baseline is zero."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        investigation_revision = self.investigation_revision

        checked_at = self.checked_at.isoformat()

        status: str = self.status

        reason = self.reason

        options = self.options.to_dict()

        baseline = self.baseline.to_dict()

        candidate = self.candidate.to_dict()

        evidence = []
        for evidence_item_data in self.evidence:
            evidence_item = evidence_item_data.to_dict()
            evidence.append(evidence_item)

        uncomparable_entries = self.uncomparable_entries

        attribution: dict[str, Any] | Unset = UNSET
        if not isinstance(self.attribution, Unset):
            attribution = self.attribution.to_dict()

        route_checks: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.route_checks, Unset):
            route_checks = []
            for route_checks_item_data in self.route_checks:
                route_checks_item = route_checks_item_data.to_dict()
                route_checks.append(route_checks_item)

        request_mix: dict[str, Any] | Unset = UNSET
        if not isinstance(self.request_mix, Unset):
            request_mix = self.request_mix.to_dict()

        baseline_requests = self.baseline_requests

        candidate_requests = self.candidate_requests

        baseline_coverage: dict[str, Any] | Unset = UNSET
        if not isinstance(self.baseline_coverage, Unset):
            baseline_coverage = self.baseline_coverage.to_dict()

        candidate_coverage: dict[str, Any] | Unset = UNSET
        if not isinstance(self.candidate_coverage, Unset):
            candidate_coverage = self.candidate_coverage.to_dict()

        total: dict[str, Any] | Unset = UNSET
        if not isinstance(self.total, Unset):
            total = self.total.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "investigation_revision": investigation_revision,
                "checked_at": checked_at,
                "status": status,
                "reason": reason,
                "options": options,
                "baseline": baseline,
                "candidate": candidate,
                "evidence": evidence,
                "uncomparable_entries": uncomparable_entries,
            }
        )
        if attribution is not UNSET:
            field_dict["attribution"] = attribution
        if route_checks is not UNSET:
            field_dict["route_checks"] = route_checks
        if request_mix is not UNSET:
            field_dict["request_mix"] = request_mix
        if baseline_requests is not UNSET:
            field_dict["baseline_requests"] = baseline_requests
        if candidate_requests is not UNSET:
            field_dict["candidate_requests"] = candidate_requests
        if baseline_coverage is not UNSET:
            field_dict["baseline_coverage"] = baseline_coverage
        if candidate_coverage is not UNSET:
            field_dict["candidate_coverage"] = candidate_coverage
        if total is not UNSET:
            field_dict["total"] = total

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.profile_attribution_comparison import ProfileAttributionComparison
        from ..models.profile_coverage import ProfileCoverage
        from ..models.profile_query import ProfileQuery
        from ..models.profile_regression_evidence import ProfileRegressionEvidence
        from ..models.profile_regression_metric import ProfileRegressionMetric
        from ..models.profile_regression_options import ProfileRegressionOptions
        from ..models.profile_request_mix_snapshot import ProfileRequestMixSnapshot
        from ..models.profile_route_regression import ProfileRouteRegression

        d = dict(src_dict)
        investigation_revision = d.pop("investigation_revision")

        checked_at = datetime.datetime.fromisoformat(d.pop("checked_at"))

        status = check_profile_regression_assessment_status(d.pop("status"))

        reason = d.pop("reason")

        options = ProfileRegressionOptions.from_dict(d.pop("options"))

        baseline = ProfileQuery.from_dict(d.pop("baseline"))

        candidate = ProfileQuery.from_dict(d.pop("candidate"))

        evidence = []
        _evidence = d.pop("evidence")
        for evidence_item_data in _evidence:
            evidence_item = ProfileRegressionEvidence.from_dict(evidence_item_data)

            evidence.append(evidence_item)

        uncomparable_entries = d.pop("uncomparable_entries")

        _attribution = d.pop("attribution", UNSET)
        attribution: ProfileAttributionComparison | Unset
        if isinstance(_attribution, Unset):
            attribution = UNSET
        else:
            attribution = ProfileAttributionComparison.from_dict(_attribution)

        _route_checks = d.pop("route_checks", UNSET)
        route_checks: list[ProfileRouteRegression] | Unset = UNSET
        if _route_checks is not UNSET:
            route_checks = []
            for route_checks_item_data in _route_checks:
                route_checks_item = ProfileRouteRegression.from_dict(route_checks_item_data)

                route_checks.append(route_checks_item)

        _request_mix = d.pop("request_mix", UNSET)
        request_mix: ProfileRequestMixSnapshot | Unset
        if isinstance(_request_mix, Unset):
            request_mix = UNSET
        else:
            request_mix = ProfileRequestMixSnapshot.from_dict(_request_mix)

        baseline_requests = d.pop("baseline_requests", UNSET)

        candidate_requests = d.pop("candidate_requests", UNSET)

        _baseline_coverage = d.pop("baseline_coverage", UNSET)
        baseline_coverage: ProfileCoverage | Unset
        if isinstance(_baseline_coverage, Unset):
            baseline_coverage = UNSET
        else:
            baseline_coverage = ProfileCoverage.from_dict(_baseline_coverage)

        _candidate_coverage = d.pop("candidate_coverage", UNSET)
        candidate_coverage: ProfileCoverage | Unset
        if isinstance(_candidate_coverage, Unset):
            candidate_coverage = UNSET
        else:
            candidate_coverage = ProfileCoverage.from_dict(_candidate_coverage)

        _total = d.pop("total", UNSET)
        total: ProfileRegressionMetric | Unset
        if isinstance(_total, Unset):
            total = UNSET
        else:
            total = ProfileRegressionMetric.from_dict(_total)

        profile_regression_assessment = cls(
            investigation_revision=investigation_revision,
            checked_at=checked_at,
            status=status,
            reason=reason,
            options=options,
            baseline=baseline,
            candidate=candidate,
            evidence=evidence,
            uncomparable_entries=uncomparable_entries,
            attribution=attribution,
            route_checks=route_checks,
            request_mix=request_mix,
            baseline_requests=baseline_requests,
            candidate_requests=candidate_requests,
            baseline_coverage=baseline_coverage,
            candidate_coverage=candidate_coverage,
            total=total,
        )

        profile_regression_assessment.additional_properties = d
        return profile_regression_assessment

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
