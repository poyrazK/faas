from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.obs_health_signal import ObsHealthSignal


T = TypeVar("T", bound="ObsHealthResponseSignals")


@_attrs_define
class ObsHealthResponseSignals:
    """Independent evidence for audit_log_write_total_5m,
    audit_log_write_failures_5m, and alerts_firing. Present on new
    servers; optional for compatibility with older responses.
    Only healthy signal values are usable. SQL-derived fields remain
    independent; a database read failure returns 503.

    """

    additional_properties: dict[str, ObsHealthSignal] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:

        field_dict: dict[str, Any] = {}
        for prop_name, prop in self.additional_properties.items():
            field_dict[prop_name] = prop.to_dict()

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.obs_health_signal import ObsHealthSignal

        d = dict(src_dict)
        obs_health_response_signals = cls()

        additional_properties = {}
        for prop_name, prop_dict in d.items():
            additional_property = ObsHealthSignal.from_dict(prop_dict)

            additional_properties[prop_name] = additional_property

        obs_health_response_signals.additional_properties = additional_properties
        return obs_health_response_signals

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> ObsHealthSignal:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: ObsHealthSignal) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
