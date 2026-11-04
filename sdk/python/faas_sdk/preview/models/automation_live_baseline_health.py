from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define

from ..models.automation_live_baseline_health_evidence_status import (
    AutomationLiveBaselineHealthEvidenceStatus,
    check_automation_live_baseline_health_evidence_status,
)
from ..models.automation_live_baseline_health_renewal_status import (
    AutomationLiveBaselineHealthRenewalStatus,
    check_automation_live_baseline_health_renewal_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.automation_live_baseline import AutomationLiveBaseline
    from ..models.automation_live_baseline_case_health import AutomationLiveBaselineCaseHealth


T = TypeVar("T", bound="AutomationLiveBaselineHealth")


@_attrs_define
class AutomationLiveBaselineHealth:
    """Coherent read-only evidence health observations for a named baseline head."""

    baseline: AutomationLiveBaseline
    """Immutable attributed receipt binding a named baseline revision to its retained suite and reviewed policy."""
    evidence_status: AutomationLiveBaselineHealthEvidenceStatus
    """Availability of current recorded reports; not a gate verdict."""
    renewal_status: AutomationLiveBaselineHealthRenewalStatus
    """Whether this same suite qualifies for a new promotion now."""
    reasons: list[str]
    outdated: bool
    cases: list[AutomationLiveBaselineCaseHealth]
    age_seconds: float | Unset = UNSET
    """Oldest original retained run age. Negative values indicate future timestamps."""
    oldest_run_created_at: datetime.datetime | Unset = UNSET
    max_age_seconds: int | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        baseline = self.baseline.to_dict()

        evidence_status: str = self.evidence_status

        renewal_status: str = self.renewal_status

        reasons = self.reasons

        outdated = self.outdated

        cases = []
        for cases_item_data in self.cases:
            cases_item = cases_item_data.to_dict()
            cases.append(cases_item)

        age_seconds = self.age_seconds

        oldest_run_created_at: str | Unset = UNSET
        if not isinstance(self.oldest_run_created_at, Unset):
            oldest_run_created_at = self.oldest_run_created_at.isoformat()

        max_age_seconds = self.max_age_seconds

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "baseline": baseline,
                "evidence_status": evidence_status,
                "renewal_status": renewal_status,
                "reasons": reasons,
                "outdated": outdated,
                "cases": cases,
            }
        )
        if age_seconds is not UNSET:
            field_dict["age_seconds"] = age_seconds
        if oldest_run_created_at is not UNSET:
            field_dict["oldest_run_created_at"] = oldest_run_created_at
        if max_age_seconds is not UNSET:
            field_dict["max_age_seconds"] = max_age_seconds

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.automation_live_baseline import AutomationLiveBaseline
        from ..models.automation_live_baseline_case_health import AutomationLiveBaselineCaseHealth

        d = dict(src_dict)
        baseline = AutomationLiveBaseline.from_dict(d.pop("baseline"))

        evidence_status = check_automation_live_baseline_health_evidence_status(d.pop("evidence_status"))

        renewal_status = check_automation_live_baseline_health_renewal_status(d.pop("renewal_status"))

        reasons = cast(list[str], d.pop("reasons"))

        outdated = d.pop("outdated")

        cases = []
        _cases = d.pop("cases")
        for cases_item_data in _cases:
            cases_item = AutomationLiveBaselineCaseHealth.from_dict(cases_item_data)

            cases.append(cases_item)

        age_seconds = d.pop("age_seconds", UNSET)

        _oldest_run_created_at = d.pop("oldest_run_created_at", UNSET)
        oldest_run_created_at: datetime.datetime | Unset
        if isinstance(_oldest_run_created_at, Unset):
            oldest_run_created_at = UNSET
        else:
            oldest_run_created_at = datetime.datetime.fromisoformat(_oldest_run_created_at)

        max_age_seconds = d.pop("max_age_seconds", UNSET)

        automation_live_baseline_health = cls(
            baseline=baseline,
            evidence_status=evidence_status,
            renewal_status=renewal_status,
            reasons=reasons,
            outdated=outdated,
            cases=cases,
            age_seconds=age_seconds,
            oldest_run_created_at=oldest_run_created_at,
            max_age_seconds=max_age_seconds,
        )

        return automation_live_baseline_health
