from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_alert_workload_group_severity import (
    ObsAlertWorkloadGroupSeverity,
    check_obs_alert_workload_group_severity,
)
from ..models.obs_alert_workload_group_status import ObsAlertWorkloadGroupStatus, check_obs_alert_workload_group_status

T = TypeVar("T", bound="ObsAlertWorkloadGroup")


@_attrs_define
class ObsAlertWorkloadGroup:
    """Nonempty retained-alert bucket for one exact status and workflow combination."""

    status: ObsAlertWorkloadGroupStatus
    severity: ObsAlertWorkloadGroupSeverity
    acknowledged: bool
    owned: bool
    snoozed: bool
    count: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        status: str = self.status

        severity: str = self.severity

        acknowledged = self.acknowledged

        owned = self.owned

        snoozed = self.snoozed

        count = self.count

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "status": status,
                "severity": severity,
                "acknowledged": acknowledged,
                "owned": owned,
                "snoozed": snoozed,
                "count": count,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        status = check_obs_alert_workload_group_status(d.pop("status"))

        severity = check_obs_alert_workload_group_severity(d.pop("severity"))

        acknowledged = d.pop("acknowledged")

        owned = d.pop("owned")

        snoozed = d.pop("snoozed")

        count = d.pop("count")

        obs_alert_workload_group = cls(
            status=status,
            severity=severity,
            acknowledged=acknowledged,
            owned=owned,
            snoozed=snoozed,
            count=count,
        )

        obs_alert_workload_group.additional_properties = d
        return obs_alert_workload_group

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
