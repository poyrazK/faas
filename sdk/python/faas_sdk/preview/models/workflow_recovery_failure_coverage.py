from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.workflow_recovery_failure_coverage_coverage import (
    WorkflowRecoveryFailureCoverageCoverage,
    check_workflow_recovery_failure_coverage_coverage,
)

T = TypeVar("T", bound="WorkflowRecoveryFailureCoverage")


@_attrs_define
class WorkflowRecoveryFailureCoverage:
    """Coverage of the bounded policy-independent terminal-failure sample in the requested workflow/time window, before
    policy filters. Counts are sample counts; partial capture and pruning never establish that no other failures
    occurred. Review correlation uses the sampled reviews.

    """

    capture_started_at: datetime.datetime
    coverage: WorkflowRecoveryFailureCoverageCoverage
    """Prospective capture and retention limits prevent a claim of complete historical coverage."""
    captured_failures: int
    reviewed_failures: int
    unreviewed_failures: int
    missing_eligibility_evidence: int
    """Sampled failures without historical retry preflight proof."""
    truncated: bool
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        capture_started_at = self.capture_started_at.isoformat()

        coverage: str = self.coverage

        captured_failures = self.captured_failures

        reviewed_failures = self.reviewed_failures

        unreviewed_failures = self.unreviewed_failures

        missing_eligibility_evidence = self.missing_eligibility_evidence

        truncated = self.truncated

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "capture_started_at": capture_started_at,
                "coverage": coverage,
                "captured_failures": captured_failures,
                "reviewed_failures": reviewed_failures,
                "unreviewed_failures": unreviewed_failures,
                "missing_eligibility_evidence": missing_eligibility_evidence,
                "truncated": truncated,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        capture_started_at = datetime.datetime.fromisoformat(d.pop("capture_started_at"))

        coverage = check_workflow_recovery_failure_coverage_coverage(d.pop("coverage"))

        captured_failures = d.pop("captured_failures")

        reviewed_failures = d.pop("reviewed_failures")

        unreviewed_failures = d.pop("unreviewed_failures")

        missing_eligibility_evidence = d.pop("missing_eligibility_evidence")

        truncated = d.pop("truncated")

        workflow_recovery_failure_coverage = cls(
            capture_started_at=capture_started_at,
            coverage=coverage,
            captured_failures=captured_failures,
            reviewed_failures=reviewed_failures,
            unreviewed_failures=unreviewed_failures,
            missing_eligibility_evidence=missing_eligibility_evidence,
            truncated=truncated,
        )

        workflow_recovery_failure_coverage.additional_properties = d
        return workflow_recovery_failure_coverage

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
