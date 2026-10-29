from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="MCPReadinessMonitorObservation")


@_attrs_define
class MCPReadinessMonitorObservation:
    """One retained observation from a continuous readiness probe."""

    observed_at: datetime.datetime
    passed: bool
    reason: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        observed_at = self.observed_at.isoformat()

        passed = self.passed

        reason = self.reason

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "observed_at": observed_at,
                "passed": passed,
            }
        )
        if reason is not UNSET:
            field_dict["reason"] = reason

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        passed = d.pop("passed")

        reason = d.pop("reason", UNSET)

        mcp_readiness_monitor_observation = cls(
            observed_at=observed_at,
            passed=passed,
            reason=reason,
        )

        return mcp_readiness_monitor_observation
