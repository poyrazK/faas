from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="AutomationFailurePolicy")


@_attrs_define
class AutomationFailurePolicy:
    """Opt-in thresholds for scheduler-enforced failure admission pausing."""

    version: int
    """Monotonic failure monitoring policy revision; zero means no policy has been configured."""
    enabled: bool
    """Whether scheduler evaluation can create a new runtime failure pause."""
    failure_threshold: int
    """Terminal failures required to latch a failure pause."""
    min_completed_runs: int
    """Minimum terminal non-cancelled sample count before failure pausing."""
    window_seconds: int
    """Lookback in seconds, bounded by the latest monitoring epoch."""

    def to_dict(self) -> dict[str, Any]:
        version = self.version

        enabled = self.enabled

        failure_threshold = self.failure_threshold

        min_completed_runs = self.min_completed_runs

        window_seconds = self.window_seconds

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "version": version,
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
        version = d.pop("version")

        enabled = d.pop("enabled")

        failure_threshold = d.pop("failure_threshold")

        min_completed_runs = d.pop("min_completed_runs")

        window_seconds = d.pop("window_seconds")

        automation_failure_policy = cls(
            version=version,
            enabled=enabled,
            failure_threshold=failure_threshold,
            min_completed_runs=min_completed_runs,
            window_seconds=window_seconds,
        )

        return automation_failure_policy
