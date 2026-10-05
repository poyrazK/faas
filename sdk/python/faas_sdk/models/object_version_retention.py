from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.object_version_retention_event_hold import (
    ObjectVersionRetentionEventHold,
    check_object_version_retention_event_hold,
)
from ..models.object_version_retention_mode import ObjectVersionRetentionMode, check_object_version_retention_mode
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.object_retention_period import ObjectRetentionPeriod


T = TypeVar("T", bound="ObjectVersionRetention")


@_attrs_define
class ObjectVersionRetention:
    """Verified native retention, or a fixed-retention intent. An empty object requests a clear; active retention cannot be
    shortened without bypass, which is unsupported. Event hold fields are observation only for this contract.

    """

    mode: ObjectVersionRetentionMode | Unset = UNSET
    retain_until_date: datetime.datetime | Unset = UNSET
    event_hold: ObjectVersionRetentionEventHold | Unset = UNSET
    event_hold_duration: ObjectRetentionPeriod | Unset = UNSET
    """Exactly one positive days or years duration must be present. Null values are rejected."""

    def to_dict(self) -> dict[str, Any]:
        mode: str | Unset = UNSET
        if not isinstance(self.mode, Unset):
            mode = self.mode

        retain_until_date: str | Unset = UNSET
        if not isinstance(self.retain_until_date, Unset):
            retain_until_date = self.retain_until_date.isoformat()

        event_hold: str | Unset = UNSET
        if not isinstance(self.event_hold, Unset):
            event_hold = self.event_hold

        event_hold_duration: dict[str, Any] | Unset = UNSET
        if not isinstance(self.event_hold_duration, Unset):
            event_hold_duration = self.event_hold_duration.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update({})
        if mode is not UNSET:
            field_dict["mode"] = mode
        if retain_until_date is not UNSET:
            field_dict["retain_until_date"] = retain_until_date
        if event_hold is not UNSET:
            field_dict["event_hold"] = event_hold
        if event_hold_duration is not UNSET:
            field_dict["event_hold_duration"] = event_hold_duration

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.object_retention_period import ObjectRetentionPeriod

        d = dict(src_dict)
        _mode = d.pop("mode", UNSET)
        mode: ObjectVersionRetentionMode | Unset
        if isinstance(_mode, Unset):
            mode = UNSET
        else:
            mode = check_object_version_retention_mode(_mode)

        _retain_until_date = d.pop("retain_until_date", UNSET)
        retain_until_date: datetime.datetime | Unset
        if isinstance(_retain_until_date, Unset):
            retain_until_date = UNSET
        else:
            retain_until_date = datetime.datetime.fromisoformat(_retain_until_date)

        _event_hold = d.pop("event_hold", UNSET)
        event_hold: ObjectVersionRetentionEventHold | Unset
        if isinstance(_event_hold, Unset):
            event_hold = UNSET
        else:
            event_hold = check_object_version_retention_event_hold(_event_hold)

        _event_hold_duration = d.pop("event_hold_duration", UNSET)
        event_hold_duration: ObjectRetentionPeriod | Unset
        if isinstance(_event_hold_duration, Unset):
            event_hold_duration = UNSET
        else:
            event_hold_duration = ObjectRetentionPeriod.from_dict(_event_hold_duration)

        object_version_retention = cls(
            mode=mode,
            retain_until_date=retain_until_date,
            event_hold=event_hold,
            event_hold_duration=event_hold_duration,
        )

        return object_version_retention
