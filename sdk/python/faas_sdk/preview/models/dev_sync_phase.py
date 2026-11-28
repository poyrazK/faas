from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.dev_sync_phase_phase import DevSyncPhasePhase, check_dev_sync_phase_phase
from ..models.dev_sync_phase_status import DevSyncPhaseStatus, check_dev_sync_phase_status
from ..types import UNSET, Unset

T = TypeVar("T", bound="DevSyncPhase")


@_attrs_define
class DevSyncPhase:
    """Safe phase-level timing from a developer sync."""

    phase: DevSyncPhasePhase
    status: DevSyncPhaseStatus
    duration_ms: int | Unset = UNSET
    reason: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        phase: str = self.phase

        status: str = self.status

        duration_ms = self.duration_ms

        reason = self.reason

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "phase": phase,
                "status": status,
            }
        )
        if duration_ms is not UNSET:
            field_dict["duration_ms"] = duration_ms
        if reason is not UNSET:
            field_dict["reason"] = reason

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        phase = check_dev_sync_phase_phase(d.pop("phase"))

        status = check_dev_sync_phase_status(d.pop("status"))

        duration_ms = d.pop("duration_ms", UNSET)

        reason = d.pop("reason", UNSET)

        dev_sync_phase = cls(
            phase=phase,
            status=status,
            duration_ms=duration_ms,
            reason=reason,
        )

        dev_sync_phase.additional_properties = d
        return dev_sync_phase

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
