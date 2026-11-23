from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.alert_observation_skip_count_reason import (
    AlertObservationSkipCountReason,
    check_alert_observation_skip_count_reason,
)

T = TypeVar("T", bound="AlertObservationSkipCount")


@_attrs_define
class AlertObservationSkipCount:
    """Count of observation checks skipped for a single reason."""

    reason: AlertObservationSkipCountReason
    count: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        reason: str = self.reason

        count = self.count

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "reason": reason,
                "count": count,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        reason = check_alert_observation_skip_count_reason(d.pop("reason"))

        count = d.pop("count")

        alert_observation_skip_count = cls(
            reason=reason,
            count=count,
        )

        alert_observation_skip_count.additional_properties = d
        return alert_observation_skip_count

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
