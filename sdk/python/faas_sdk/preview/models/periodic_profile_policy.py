from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="PeriodicProfilePolicy")


@_attrs_define
class PeriodicProfilePolicy:
    """Opt-in monitoring of live completed deployments and explicit routes. Interval must be a whole number of minutes and
    at least window_seconds. Each route pins its first evidence-qualified window on this deployment and policy revision.
    Both regression and recovery need this many consecutive supported, distinct-window results; missing evidence
    interrupts confirmation. Expired baselines start a new context without recovering the old incident.

    """

    interval_seconds: int
    confirmations: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        interval_seconds = self.interval_seconds

        confirmations = self.confirmations

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "interval_seconds": interval_seconds,
                "confirmations": confirmations,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        interval_seconds = d.pop("interval_seconds")

        confirmations = d.pop("confirmations")

        periodic_profile_policy = cls(
            interval_seconds=interval_seconds,
            confirmations=confirmations,
        )

        periodic_profile_policy.additional_properties = d
        return periodic_profile_policy

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
