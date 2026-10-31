from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.debug_request_correlation_stage_phase import (
    DebugRequestCorrelationStagePhase,
    check_debug_request_correlation_stage_phase,
)
from ..models.debug_request_correlation_stage_status import (
    DebugRequestCorrelationStageStatus,
    check_debug_request_correlation_stage_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="DebugRequestCorrelationStage")


@_attrs_define
class DebugRequestCorrelationStage:
    """One bounded edge-to-billing request stage. Missing and partial signals are explicit and are never rendered as zero
    latency.

    """

    phase: DebugRequestCorrelationStagePhase
    status: DebugRequestCorrelationStageStatus
    started_at: datetime.datetime | Unset = UNSET
    completed_at: datetime.datetime | Unset = UNSET
    duration_ms: int | Unset = UNSET
    evidence_count: int | Unset = UNSET
    reason: str | Unset = UNSET
    approximate: bool | Unset = UNSET
    """True when the stage is derived from a collapsed request bucket rather than an exact request timestamp."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        phase: str = self.phase

        status: str = self.status

        started_at: str | Unset = UNSET
        if not isinstance(self.started_at, Unset):
            started_at = self.started_at.isoformat()

        completed_at: str | Unset = UNSET
        if not isinstance(self.completed_at, Unset):
            completed_at = self.completed_at.isoformat()

        duration_ms = self.duration_ms

        evidence_count = self.evidence_count

        reason = self.reason

        approximate = self.approximate

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "phase": phase,
                "status": status,
            }
        )
        if started_at is not UNSET:
            field_dict["started_at"] = started_at
        if completed_at is not UNSET:
            field_dict["completed_at"] = completed_at
        if duration_ms is not UNSET:
            field_dict["duration_ms"] = duration_ms
        if evidence_count is not UNSET:
            field_dict["evidence_count"] = evidence_count
        if reason is not UNSET:
            field_dict["reason"] = reason
        if approximate is not UNSET:
            field_dict["approximate"] = approximate

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        phase = check_debug_request_correlation_stage_phase(d.pop("phase"))

        status = check_debug_request_correlation_stage_status(d.pop("status"))

        _started_at = d.pop("started_at", UNSET)
        started_at: datetime.datetime | Unset
        if isinstance(_started_at, Unset):
            started_at = UNSET
        else:
            started_at = datetime.datetime.fromisoformat(_started_at)

        _completed_at = d.pop("completed_at", UNSET)
        completed_at: datetime.datetime | Unset
        if isinstance(_completed_at, Unset):
            completed_at = UNSET
        else:
            completed_at = datetime.datetime.fromisoformat(_completed_at)

        duration_ms = d.pop("duration_ms", UNSET)

        evidence_count = d.pop("evidence_count", UNSET)

        reason = d.pop("reason", UNSET)

        approximate = d.pop("approximate", UNSET)

        debug_request_correlation_stage = cls(
            phase=phase,
            status=status,
            started_at=started_at,
            completed_at=completed_at,
            duration_ms=duration_ms,
            evidence_count=evidence_count,
            reason=reason,
            approximate=approximate,
        )

        debug_request_correlation_stage.additional_properties = d
        return debug_request_correlation_stage

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
