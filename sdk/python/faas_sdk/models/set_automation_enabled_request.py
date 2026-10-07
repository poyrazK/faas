from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="SetAutomationEnabledRequest")


@_attrs_define
class SetAutomationEnabledRequest:
    """Publication revision and the desired state of automatic starts."""

    expected_version: int
    """Current automation revision to pause or resume."""
    enabled: bool

    def to_dict(self) -> dict[str, Any]:
        expected_version = self.expected_version

        enabled = self.enabled

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "expected_version": expected_version,
                "enabled": enabled,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        expected_version = d.pop("expected_version")

        enabled = d.pop("enabled")

        set_automation_enabled_request = cls(
            expected_version=expected_version,
            enabled=enabled,
        )

        return set_automation_enabled_request
