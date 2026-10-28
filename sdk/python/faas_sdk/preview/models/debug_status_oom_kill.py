from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="DebugStatusOOMKill")


@_attrs_define
class DebugStatusOOMKill:
    """One out-of-memory kill of an app instance. A deployment that was not yet live fails; a live release keeps serving on
    a new instance.

    """

    at: datetime.datetime
    deployment_id: UUID
    peak_mb: int
    plan_mb: int
    deployment_failed: bool
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        at = self.at.isoformat()

        deployment_id = str(self.deployment_id)

        peak_mb = self.peak_mb

        plan_mb = self.plan_mb

        deployment_failed = self.deployment_failed

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "at": at,
                "deployment_id": deployment_id,
                "peak_mb": peak_mb,
                "plan_mb": plan_mb,
                "deployment_failed": deployment_failed,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        at = datetime.datetime.fromisoformat(d.pop("at"))

        deployment_id = UUID(d.pop("deployment_id"))

        peak_mb = d.pop("peak_mb")

        plan_mb = d.pop("plan_mb")

        deployment_failed = d.pop("deployment_failed")

        debug_status_oom_kill = cls(
            at=at,
            deployment_id=deployment_id,
            peak_mb=peak_mb,
            plan_mb=plan_mb,
            deployment_failed=deployment_failed,
        )

        debug_status_oom_kill.additional_properties = d
        return debug_status_oom_kill

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
