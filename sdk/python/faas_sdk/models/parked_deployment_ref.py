from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.parked_deployment_ref_parked_reason import (
    ParkedDeploymentRefParkedReason,
    check_parked_deployment_ref_parked_reason,
)

T = TypeVar("T", bound="ParkedDeploymentRef")


@_attrs_define
class ParkedDeploymentRef:
    """Reference to a deployment that was parked (issue #554 / ADR-079 follow-up). Returned in
    AppResponse.parked_deployment when the app has at least one parked deployment. The `parked_reason` field is closed-
    set (liveness_exhausted | lifecycle_park | admin_park | security_scan_regressed) — enforced at the schema layer via
    the deployments_parked_reason_check constraint.

    """

    id: str
    parked_reason: ParkedDeploymentRefParkedReason
    """Closed-set parking reason from the schema-layer CHECK constraint."""
    parked_at: datetime.datetime
    """Wall-clock timestamp the deployment was parked (set once, idempotent across schedd restart cycles)."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        parked_reason: str = self.parked_reason

        parked_at = self.parked_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "parked_reason": parked_reason,
                "parked_at": parked_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = d.pop("id")

        parked_reason = check_parked_deployment_ref_parked_reason(d.pop("parked_reason"))

        parked_at = datetime.datetime.fromisoformat(d.pop("parked_at"))

        parked_deployment_ref = cls(
            id=id,
            parked_reason=parked_reason,
            parked_at=parked_at,
        )

        parked_deployment_ref.additional_properties = d
        return parked_deployment_ref

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
