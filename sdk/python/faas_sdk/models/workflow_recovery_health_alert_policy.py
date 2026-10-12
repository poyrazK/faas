from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="WorkflowRecoveryHealthAlertPolicy")


@_attrs_define
class WorkflowRecoveryHealthAlertPolicy:
    """Opt-in recovery health incident notifications with explicit scheduler evaluation and sustained unhealthy duration
    thresholds. Settings are replaced with the full recovery policy. Read-only health query thresholds remain request-
    local.

    """

    enabled: bool
    stall_after_seconds: int
    alert_after_seconds: int

    def to_dict(self) -> dict[str, Any]:
        enabled = self.enabled

        stall_after_seconds = self.stall_after_seconds

        alert_after_seconds = self.alert_after_seconds

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "enabled": enabled,
                "stall_after_seconds": stall_after_seconds,
                "alert_after_seconds": alert_after_seconds,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        enabled = d.pop("enabled")

        stall_after_seconds = d.pop("stall_after_seconds")

        alert_after_seconds = d.pop("alert_after_seconds")

        workflow_recovery_health_alert_policy = cls(
            enabled=enabled,
            stall_after_seconds=stall_after_seconds,
            alert_after_seconds=alert_after_seconds,
        )

        return workflow_recovery_health_alert_policy
