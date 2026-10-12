from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.realtime_replay_job_outcome_status import (
    RealtimeReplayJobOutcomeStatus,
    check_realtime_replay_job_outcome_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="RealtimeReplayJobOutcome")


@_attrs_define
class RealtimeReplayJobOutcome:
    """Realtime Replay Job Outcome."""

    sequence: str
    status: RealtimeReplayJobOutcomeStatus
    checked_at: datetime.datetime | Unset = UNSET
    """Per-item observation time; absent in older retained job outcomes."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        sequence = self.sequence

        status: str = self.status

        checked_at: str | Unset = UNSET
        if not isinstance(self.checked_at, Unset):
            checked_at = self.checked_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "sequence": sequence,
                "status": status,
            }
        )
        if checked_at is not UNSET:
            field_dict["checked_at"] = checked_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        sequence = d.pop("sequence")

        status = check_realtime_replay_job_outcome_status(d.pop("status"))

        _checked_at = d.pop("checked_at", UNSET)
        checked_at: datetime.datetime | Unset
        if isinstance(_checked_at, Unset):
            checked_at = UNSET
        else:
            checked_at = datetime.datetime.fromisoformat(_checked_at)

        realtime_replay_job_outcome = cls(
            sequence=sequence,
            status=status,
            checked_at=checked_at,
        )

        realtime_replay_job_outcome.additional_properties = d
        return realtime_replay_job_outcome

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
