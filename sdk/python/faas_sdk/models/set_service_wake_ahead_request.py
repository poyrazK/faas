from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="SetServiceWakeAheadRequest")


@_attrs_define
class SetServiceWakeAheadRequest:
    """Turns service wake-ahead on or off for one app."""

    enabled: bool
    """true to wake measured services ahead, false to wake them only when called."""

    def to_dict(self) -> dict[str, Any]:
        enabled = self.enabled

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "enabled": enabled,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        enabled = d.pop("enabled")

        set_service_wake_ahead_request = cls(
            enabled=enabled,
        )

        return set_service_wake_ahead_request
