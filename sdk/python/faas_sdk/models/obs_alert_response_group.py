from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_alert_response_group_severity import (
    ObsAlertResponseGroupSeverity,
    check_obs_alert_response_group_severity,
)

if TYPE_CHECKING:
    from ..models.obs_alert_response_aggregate import ObsAlertResponseAggregate


T = TypeVar("T", bound="ObsAlertResponseGroup")


@_attrs_define
class ObsAlertResponseGroup:
    """One nonempty severity/component timing group with no actor identities or raw audit data."""

    severity: ObsAlertResponseGroupSeverity
    component: str
    metrics: ObsAlertResponseAggregate
    """One cohort denominator shared by independent acknowledgment and observed recovery classifications."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        severity: str = self.severity

        component = self.component

        metrics = self.metrics.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "severity": severity,
                "component": component,
                "metrics": metrics,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.obs_alert_response_aggregate import ObsAlertResponseAggregate

        d = dict(src_dict)
        severity = check_obs_alert_response_group_severity(d.pop("severity"))

        component = d.pop("component")

        metrics = ObsAlertResponseAggregate.from_dict(d.pop("metrics"))

        obs_alert_response_group = cls(
            severity=severity,
            component=component,
            metrics=metrics,
        )

        obs_alert_response_group.additional_properties = d
        return obs_alert_response_group

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
