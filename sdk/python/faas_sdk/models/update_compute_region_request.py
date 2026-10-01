from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="UpdateComputeRegionRequest")


@_attrs_define
class UpdateComputeRegionRequest:
    """Complete operator-controlled compute region policy."""

    display_name: str
    """Human-readable name without surrounding whitespace or control characters."""
    accepting_new_apps: bool
    """Explicit new-app admission policy."""

    def to_dict(self) -> dict[str, Any]:
        display_name = self.display_name

        accepting_new_apps = self.accepting_new_apps

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "display_name": display_name,
                "accepting_new_apps": accepting_new_apps,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        display_name = d.pop("display_name")

        accepting_new_apps = d.pop("accepting_new_apps")

        update_compute_region_request = cls(
            display_name=display_name,
            accepting_new_apps=accepting_new_apps,
        )

        return update_compute_region_request
