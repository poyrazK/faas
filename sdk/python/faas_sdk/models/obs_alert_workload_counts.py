from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ObsAlertWorkloadCounts")


@_attrs_define
class ObsAlertWorkloadCounts:
    """Whole-retention episode totals and an attention union that counts each episode once."""

    total: int
    firing: int
    unknown: int
    resolved: int
    needs_attention: int
    """Unsnoozed unresolved episodes that are unacknowledged or unowned; unknown status is included."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        total = self.total

        firing = self.firing

        unknown = self.unknown

        resolved = self.resolved

        needs_attention = self.needs_attention

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "total": total,
                "firing": firing,
                "unknown": unknown,
                "resolved": resolved,
                "needs_attention": needs_attention,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        total = d.pop("total")

        firing = d.pop("firing")

        unknown = d.pop("unknown")

        resolved = d.pop("resolved")

        needs_attention = d.pop("needs_attention")

        obs_alert_workload_counts = cls(
            total=total,
            firing=firing,
            unknown=unknown,
            resolved=resolved,
            needs_attention=needs_attention,
        )

        obs_alert_workload_counts.additional_properties = d
        return obs_alert_workload_counts

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
