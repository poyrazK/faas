from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="SetAutomationFailurePolicyRequest")


@_attrs_define
class SetAutomationFailurePolicyRequest:
    """Complete failure policy replacement guarded by an expected version."""

    expected_version: int
    """Failure policy revision the caller intends to replace."""
    enabled: bool
    """Requested setting controlling whether scheduler evaluation can create a new runtime failure pause."""
    failure_threshold: int
    """Requested number of terminal failures required to latch a failure pause."""
    min_completed_runs: int
    """Requested minimum terminal non-cancelled sample count before failure pausing."""
    window_seconds: int
    """Requested lookback in seconds, bounded by the latest monitoring epoch."""

    def to_dict(self) -> dict[str, Any]:
        expected_version = self.expected_version

        enabled = self.enabled

        failure_threshold = self.failure_threshold

        min_completed_runs = self.min_completed_runs

        window_seconds = self.window_seconds

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "expected_version": expected_version,
                "enabled": enabled,
                "failure_threshold": failure_threshold,
                "min_completed_runs": min_completed_runs,
                "window_seconds": window_seconds,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        expected_version = d.pop("expected_version")

        enabled = d.pop("enabled")

        failure_threshold = d.pop("failure_threshold")

        min_completed_runs = d.pop("min_completed_runs")

        window_seconds = d.pop("window_seconds")

        set_automation_failure_policy_request = cls(
            expected_version=expected_version,
            enabled=enabled,
            failure_threshold=failure_threshold,
            min_completed_runs=min_completed_runs,
            window_seconds=window_seconds,
        )

        return set_automation_failure_policy_request
