from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ObsHealthResponseTraceIdCompletenessRatio")


@_attrs_define
class ObsHealthResponseTraceIdCompletenessRatio:
    """Per-kind ratio of operator.action.* events with a
    non-NULL trace_id over all operator.action.* events
    in the window. Kinds with zero rows are seeded to
    1.0 (vacuous truth — see Store interface comment).
    Reads events (live), NOT audit_log (FK-free
    post-deletion copy) — ADR-091 §3.7.4.

        Example:
            {'force_park': 1.0, 'force_cold_boot': 1.0, 'force_restart': 1.0}

    """

    additional_properties: dict[str, float] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        obs_health_response_trace_id_completeness_ratio = cls()

        obs_health_response_trace_id_completeness_ratio.additional_properties = d
        return obs_health_response_trace_id_completeness_ratio

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> float:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: float) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
