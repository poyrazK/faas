from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="EventRecoveryControlRequest")


@_attrs_define
class EventRecoveryControlRequest:
    """Optional audit reason attached to a recovery pause, resume or cancellation."""

    reason: str | Unset = UNSET
    """Optional operator reason, at most 512 UTF-8 bytes without control characters."""

    def to_dict(self) -> dict[str, Any]:
        reason = self.reason

        field_dict: dict[str, Any] = {}

        field_dict.update({})
        if reason is not UNSET:
            field_dict["reason"] = reason

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        reason = d.pop("reason", UNSET)

        event_recovery_control_request = cls(
            reason=reason,
        )

        return event_recovery_control_request
