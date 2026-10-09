from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="AppHealthCapacity")


@_attrs_define
class AppHealthCapacity:
    """Measured serving replica counts, with explicit evidence availability."""

    known: bool
    """False when instance evidence is unavailable or truncated."""
    required: int
    ready: int
    starting: int
    unready: int
    unknown: int

    def to_dict(self) -> dict[str, Any]:
        known = self.known

        required = self.required

        ready = self.ready

        starting = self.starting

        unready = self.unready

        unknown = self.unknown

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "known": known,
                "required": required,
                "ready": ready,
                "starting": starting,
                "unready": unready,
                "unknown": unknown,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        known = d.pop("known")

        required = d.pop("required")

        ready = d.pop("ready")

        starting = d.pop("starting")

        unready = d.pop("unready")

        unknown = d.pop("unknown")

        app_health_capacity = cls(
            known=known,
            required=required,
            ready=ready,
            starting=starting,
            unready=unready,
            unknown=unknown,
        )

        return app_health_capacity
