from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.canary_profile_signal_metric import CanaryProfileSignalMetric, check_canary_profile_signal_metric
from ..models.canary_profile_signal_mode import CanaryProfileSignalMode, check_canary_profile_signal_mode
from ..models.canary_profile_signal_status import CanaryProfileSignalStatus, check_canary_profile_signal_status
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.profile_attribution_comparison import ProfileAttributionComparison
    from ..models.profile_canary_gate_state import ProfileCanaryGateState
    from ..models.profile_coverage import ProfileCoverage
    from ..models.profile_query import ProfileQuery
    from ..models.profile_regression_evidence import ProfileRegressionEvidence
    from ..models.profile_regression_metric import ProfileRegressionMetric
    from ..models.profile_regression_options import ProfileRegressionOptions
    from ..models.profile_request_mix_snapshot import ProfileRequestMixSnapshot
    from ..models.profile_route_regression import ProfileRouteRegression
    from ..models.profile_source import ProfileSource


T = TypeVar("T", bound="CanaryProfileSignal")


@_attrs_define
class CanaryProfileSignal:
    """Latest retained sampled CPU comparison for a deployment's canary stages, produced by a background worker using the
    app's enabled automatic profile policy and equal fixed windows. The stage and policy revision identify the
    assessment. Route-health reads return the saved result and never query profile storage. Checks are advisory unless
    canary_gate is explicitly configured. Gate evidence requires consecutive distinct qualified route windows; timeout
    behavior is configured explicitly. Completed assessments are retained for 30 days.

    """

    mode: CanaryProfileSignalMode
    status: CanaryProfileSignalStatus
    reason: str
    canary_step: int
    canary_step_started_at: datetime.datetime
    created_at: datetime.datetime
    attempts: int
    policy_revision: int
    metric: CanaryProfileSignalMetric
    window_seconds: int
    options: ProfileRegressionOptions
    """Threshold policy for sampled CPU per wall-clock second or per weighted observed request. Both relative and
    selected-metric absolute increase thresholds must be met. CPU-per-request mode also requires retained request
    telemetry and its configured minimum request count in both deployment windows; absent or sparse counts are
    inconclusive. Request telemetry rows use minute-bucket timestamps, so counts near window boundaries can be
    approximate and may be incomplete. Capture requirements apply separately to each profile window."""
    evidence: list[ProfileRegressionEvidence]
    uncomparable_entries: int
    gate: ProfileCanaryGateState | Unset = UNSET
    """Retained stage evidence and bounded route streaks used to qualify the profiling gate."""
    attribution: ProfileAttributionComparison | Unset = UNSET
    """Captured route attribution quality comparison. An absolute labeled-share change of at least 20 percentage
    points suppresses advisory route regression conclusions without changing aggregate results or rollout behavior.
    Background work and traffic changes can also change labeled CPU share."""
    route_checks: list[ProfileRouteRegression] | Unset = UNSET
    request_mix: ProfileRequestMixSnapshot | Unset = UNSET
    """Frozen observed telemetry summary, at most 16 KiB JSON. Completeness describes route/status aggregation, not
    telemetry delivery. Readable with the assessment after raw request telemetry expires."""
    checked_at: datetime.datetime | Unset = UNSET
    """Last completed assessment attempt, absent until the worker has queried both profile windows."""
    next_attempt_at: datetime.datetime | Unset = UNSET
    """Scheduled first check or retry time while the result is pending."""
    completed_at: datetime.datetime | Unset = UNSET
    baseline: ProfileQuery | Unset = UNSET
    """Authorized deployment CPU capture window."""
    candidate: ProfileQuery | Unset = UNSET
    """Authorized deployment CPU capture window."""
    baseline_requests: int | Unset = UNSET
    candidate_requests: int | Unset = UNSET
    baseline_coverage: ProfileCoverage | Unset = UNSET
    """Recorded collection evidence. Overlapping intervals count once; gaps can include idle time or loss. Failure
    counts are a lower bound; losses before ingestion are unknown. Unavailable coverage must not be interpreted as
    zero collection."""
    candidate_coverage: ProfileCoverage | Unset = UNSET
    """Recorded collection evidence. Overlapping intervals count once; gaps can include idle time or loss. Failure
    counts are a lower bound; losses before ingestion are unknown. Unavailable coverage must not be interpreted as
    zero collection."""
    baseline_source: ProfileSource | Unset = UNSET
    """Deployment-recorded GitHub provenance. Links use a full immutable commit SHA; uploaded or generated source
    is not verified against that commit. Unavailable provenance is explicit."""
    candidate_source: ProfileSource | Unset = UNSET
    """Deployment-recorded GitHub provenance. Links use a full immutable commit SHA; uploaded or generated source
    is not verified against that commit. Unavailable provenance is explicit."""
    comparison_url: str | Unset = UNSET
    """Dashboard link to load the exact stable and canary profile windows for a completed assessment."""
    total: ProfileRegressionMetric | Unset = UNSET
    """Observed sampled CPU/s comparison, optionally with CPU seconds per weighted observed request.
    exceeds_threshold and relative_increase_percent use the metric selected by options. The relative percentage is
    absent when that metric's baseline is zero."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        mode: str = self.mode

        status: str = self.status

        reason = self.reason

        canary_step = self.canary_step

        canary_step_started_at = self.canary_step_started_at.isoformat()

        created_at = self.created_at.isoformat()

        attempts = self.attempts

        policy_revision = self.policy_revision

        metric: str = self.metric

        window_seconds = self.window_seconds

        options = self.options.to_dict()

        evidence = []
        for evidence_item_data in self.evidence:
            evidence_item = evidence_item_data.to_dict()
            evidence.append(evidence_item)

        uncomparable_entries = self.uncomparable_entries

        gate: dict[str, Any] | Unset = UNSET
        if not isinstance(self.gate, Unset):
            gate = self.gate.to_dict()

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

        checked_at: str | Unset = UNSET
        if not isinstance(self.checked_at, Unset):
            checked_at = self.checked_at.isoformat()

        next_attempt_at: str | Unset = UNSET
        if not isinstance(self.next_attempt_at, Unset):
            next_attempt_at = self.next_attempt_at.isoformat()

        completed_at: str | Unset = UNSET
        if not isinstance(self.completed_at, Unset):
            completed_at = self.completed_at.isoformat()

        baseline: dict[str, Any] | Unset = UNSET
        if not isinstance(self.baseline, Unset):
            baseline = self.baseline.to_dict()

        candidate: dict[str, Any] | Unset = UNSET
        if not isinstance(self.candidate, Unset):
            candidate = self.candidate.to_dict()

        baseline_requests = self.baseline_requests

        candidate_requests = self.candidate_requests

        baseline_coverage: dict[str, Any] | Unset = UNSET
        if not isinstance(self.baseline_coverage, Unset):
            baseline_coverage = self.baseline_coverage.to_dict()

        candidate_coverage: dict[str, Any] | Unset = UNSET
        if not isinstance(self.candidate_coverage, Unset):
            candidate_coverage = self.candidate_coverage.to_dict()

        baseline_source: dict[str, Any] | Unset = UNSET
        if not isinstance(self.baseline_source, Unset):
            baseline_source = self.baseline_source.to_dict()

        candidate_source: dict[str, Any] | Unset = UNSET
        if not isinstance(self.candidate_source, Unset):
            candidate_source = self.candidate_source.to_dict()

        comparison_url = self.comparison_url

        total: dict[str, Any] | Unset = UNSET
        if not isinstance(self.total, Unset):
            total = self.total.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "mode": mode,
                "status": status,
                "reason": reason,
                "canary_step": canary_step,
                "canary_step_started_at": canary_step_started_at,
                "created_at": created_at,
                "attempts": attempts,
                "policy_revision": policy_revision,
                "metric": metric,
                "window_seconds": window_seconds,
                "options": options,
                "evidence": evidence,
                "uncomparable_entries": uncomparable_entries,
            }
        )
        if gate is not UNSET:
            field_dict["gate"] = gate
        if attribution is not UNSET:
            field_dict["attribution"] = attribution
        if route_checks is not UNSET:
            field_dict["route_checks"] = route_checks
        if request_mix is not UNSET:
            field_dict["request_mix"] = request_mix
        if checked_at is not UNSET:
            field_dict["checked_at"] = checked_at
        if next_attempt_at is not UNSET:
            field_dict["next_attempt_at"] = next_attempt_at
        if completed_at is not UNSET:
            field_dict["completed_at"] = completed_at
        if baseline is not UNSET:
            field_dict["baseline"] = baseline
        if candidate is not UNSET:
            field_dict["candidate"] = candidate
        if baseline_requests is not UNSET:
            field_dict["baseline_requests"] = baseline_requests
        if candidate_requests is not UNSET:
            field_dict["candidate_requests"] = candidate_requests
        if baseline_coverage is not UNSET:
            field_dict["baseline_coverage"] = baseline_coverage
        if candidate_coverage is not UNSET:
            field_dict["candidate_coverage"] = candidate_coverage
        if baseline_source is not UNSET:
            field_dict["baseline_source"] = baseline_source
        if candidate_source is not UNSET:
            field_dict["candidate_source"] = candidate_source
        if comparison_url is not UNSET:
            field_dict["comparison_url"] = comparison_url
        if total is not UNSET:
            field_dict["total"] = total

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.profile_attribution_comparison import ProfileAttributionComparison
        from ..models.profile_canary_gate_state import ProfileCanaryGateState
        from ..models.profile_coverage import ProfileCoverage
        from ..models.profile_query import ProfileQuery
        from ..models.profile_regression_evidence import ProfileRegressionEvidence
        from ..models.profile_regression_metric import ProfileRegressionMetric
        from ..models.profile_regression_options import ProfileRegressionOptions
        from ..models.profile_request_mix_snapshot import ProfileRequestMixSnapshot
        from ..models.profile_route_regression import ProfileRouteRegression
        from ..models.profile_source import ProfileSource

        d = dict(src_dict)
        mode = check_canary_profile_signal_mode(d.pop("mode"))

        status = check_canary_profile_signal_status(d.pop("status"))

        reason = d.pop("reason")

        canary_step = d.pop("canary_step")

        canary_step_started_at = datetime.datetime.fromisoformat(d.pop("canary_step_started_at"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        attempts = d.pop("attempts")

        policy_revision = d.pop("policy_revision")

        metric = check_canary_profile_signal_metric(d.pop("metric"))

        window_seconds = d.pop("window_seconds")

        options = ProfileRegressionOptions.from_dict(d.pop("options"))

        evidence = []
        _evidence = d.pop("evidence")
        for evidence_item_data in _evidence:
            evidence_item = ProfileRegressionEvidence.from_dict(evidence_item_data)

            evidence.append(evidence_item)

        uncomparable_entries = d.pop("uncomparable_entries")

        _gate = d.pop("gate", UNSET)
        gate: ProfileCanaryGateState | Unset
        if isinstance(_gate, Unset):
            gate = UNSET
        else:
            gate = ProfileCanaryGateState.from_dict(_gate)

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

        _checked_at = d.pop("checked_at", UNSET)
        checked_at: datetime.datetime | Unset
        if isinstance(_checked_at, Unset):
            checked_at = UNSET
        else:
            checked_at = datetime.datetime.fromisoformat(_checked_at)

        _next_attempt_at = d.pop("next_attempt_at", UNSET)
        next_attempt_at: datetime.datetime | Unset
        if isinstance(_next_attempt_at, Unset):
            next_attempt_at = UNSET
        else:
            next_attempt_at = datetime.datetime.fromisoformat(_next_attempt_at)

        _completed_at = d.pop("completed_at", UNSET)
        completed_at: datetime.datetime | Unset
        if isinstance(_completed_at, Unset):
            completed_at = UNSET
        else:
            completed_at = datetime.datetime.fromisoformat(_completed_at)

        _baseline = d.pop("baseline", UNSET)
        baseline: ProfileQuery | Unset
        if isinstance(_baseline, Unset):
            baseline = UNSET
        else:
            baseline = ProfileQuery.from_dict(_baseline)

        _candidate = d.pop("candidate", UNSET)
        candidate: ProfileQuery | Unset
        if isinstance(_candidate, Unset):
            candidate = UNSET
        else:
            candidate = ProfileQuery.from_dict(_candidate)

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

        _baseline_source = d.pop("baseline_source", UNSET)
        baseline_source: ProfileSource | Unset
        if isinstance(_baseline_source, Unset):
            baseline_source = UNSET
        else:
            baseline_source = ProfileSource.from_dict(_baseline_source)

        _candidate_source = d.pop("candidate_source", UNSET)
        candidate_source: ProfileSource | Unset
        if isinstance(_candidate_source, Unset):
            candidate_source = UNSET
        else:
            candidate_source = ProfileSource.from_dict(_candidate_source)

        comparison_url = d.pop("comparison_url", UNSET)

        _total = d.pop("total", UNSET)
        total: ProfileRegressionMetric | Unset
        if isinstance(_total, Unset):
            total = UNSET
        else:
            total = ProfileRegressionMetric.from_dict(_total)

        canary_profile_signal = cls(
            mode=mode,
            status=status,
            reason=reason,
            canary_step=canary_step,
            canary_step_started_at=canary_step_started_at,
            created_at=created_at,
            attempts=attempts,
            policy_revision=policy_revision,
            metric=metric,
            window_seconds=window_seconds,
            options=options,
            evidence=evidence,
            uncomparable_entries=uncomparable_entries,
            gate=gate,
            attribution=attribution,
            route_checks=route_checks,
            request_mix=request_mix,
            checked_at=checked_at,
            next_attempt_at=next_attempt_at,
            completed_at=completed_at,
            baseline=baseline,
            candidate=candidate,
            baseline_requests=baseline_requests,
            candidate_requests=candidate_requests,
            baseline_coverage=baseline_coverage,
            candidate_coverage=candidate_coverage,
            baseline_source=baseline_source,
            candidate_source=candidate_source,
            comparison_url=comparison_url,
            total=total,
        )

        canary_profile_signal.additional_properties = d
        return canary_profile_signal

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
