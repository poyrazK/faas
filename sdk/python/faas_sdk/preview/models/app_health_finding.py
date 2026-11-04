from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.app_health_finding_reason import AppHealthFindingReason, check_app_health_finding_reason
from ..models.app_health_finding_status import AppHealthFindingStatus, check_app_health_finding_status
from ..types import UNSET, Unset

T = TypeVar("T", bound="AppHealthFinding")


@_attrs_define
class AppHealthFinding:
    """A safe, independent readiness finding for a serving replica."""

    reason: AppHealthFindingReason
    status: AppHealthFindingStatus
    detail: str
    """Safe explanation without probe output or infrastructure addresses."""
    deployment_id: str
    instance_id: str
    source: str
    """node, configuration, primary_app, or sidecar followed by a colon and the declared companion name."""
    observed_at: datetime.datetime | Unset = UNSET
    """Recorded readiness transition or last node heartbeat; absent for missing evidence. A transition time is not
    a probe heartbeat."""

    def to_dict(self) -> dict[str, Any]:
        reason: str = self.reason

        status: str = self.status

        detail = self.detail

        deployment_id = self.deployment_id

        instance_id = self.instance_id

        source = self.source

        observed_at: str | Unset = UNSET
        if not isinstance(self.observed_at, Unset):
            observed_at = self.observed_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "reason": reason,
                "status": status,
                "detail": detail,
                "deployment_id": deployment_id,
                "instance_id": instance_id,
                "source": source,
            }
        )
        if observed_at is not UNSET:
            field_dict["observed_at"] = observed_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        reason = check_app_health_finding_reason(d.pop("reason"))

        status = check_app_health_finding_status(d.pop("status"))

        detail = d.pop("detail")

        deployment_id = d.pop("deployment_id")

        instance_id = d.pop("instance_id")

        source = d.pop("source")

        _observed_at = d.pop("observed_at", UNSET)
        observed_at: datetime.datetime | Unset
        if isinstance(_observed_at, Unset):
            observed_at = UNSET
        else:
            observed_at = datetime.datetime.fromisoformat(_observed_at)

        app_health_finding = cls(
            reason=reason,
            status=status,
            detail=detail,
            deployment_id=deployment_id,
            instance_id=instance_id,
            source=source,
            observed_at=observed_at,
        )

        return app_health_finding
