from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="EnableEdgeAttackModeRequest")


@_attrs_define
class EnableEdgeAttackModeRequest:
    """Switches attack mode on for a duration, or moves the end of an active one."""

    duration_seconds: int | Unset = 3600
    """How long attack mode stays on from now. Switching it on while it is on restarts the clock with this
    duration."""

    def to_dict(self) -> dict[str, Any]:
        duration_seconds = self.duration_seconds

        field_dict: dict[str, Any] = {}

        field_dict.update({})
        if duration_seconds is not UNSET:
            field_dict["duration_seconds"] = duration_seconds

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        duration_seconds = d.pop("duration_seconds", UNSET)

        enable_edge_attack_mode_request = cls(
            duration_seconds=duration_seconds,
        )

        return enable_edge_attack_mode_request
