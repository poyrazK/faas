from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.object_lock_default_retention_mode import (
    ObjectLockDefaultRetentionMode,
    check_object_lock_default_retention_mode,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.object_retention_period import ObjectRetentionPeriod


T = TypeVar("T", bound="ObjectLockDefaultRetention")


@_attrs_define
class ObjectLockDefaultRetention:
    """A mode with exactly one fixed duration, an event hold duration or both. Null values and an empty default are
    rejected.

    """

    mode: ObjectLockDefaultRetentionMode
    days: int | Unset = UNSET
    years: int | Unset = UNSET
    default_event_hold: ObjectRetentionPeriod | Unset = UNSET
    """Exactly one positive days or years duration must be present. Null values are rejected."""

    def to_dict(self) -> dict[str, Any]:
        mode: str = self.mode

        days = self.days

        years = self.years

        default_event_hold: dict[str, Any] | Unset = UNSET
        if not isinstance(self.default_event_hold, Unset):
            default_event_hold = self.default_event_hold.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "mode": mode,
            }
        )
        if days is not UNSET:
            field_dict["days"] = days
        if years is not UNSET:
            field_dict["years"] = years
        if default_event_hold is not UNSET:
            field_dict["default_event_hold"] = default_event_hold

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.object_retention_period import ObjectRetentionPeriod

        d = dict(src_dict)
        mode = check_object_lock_default_retention_mode(d.pop("mode"))

        days = d.pop("days", UNSET)

        years = d.pop("years", UNSET)

        _default_event_hold = d.pop("default_event_hold", UNSET)
        default_event_hold: ObjectRetentionPeriod | Unset
        if isinstance(_default_event_hold, Unset):
            default_event_hold = UNSET
        else:
            default_event_hold = ObjectRetentionPeriod.from_dict(_default_event_hold)

        object_lock_default_retention = cls(
            mode=mode,
            days=days,
            years=years,
            default_event_hold=default_event_hold,
        )

        return object_lock_default_retention
