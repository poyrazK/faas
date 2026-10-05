from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="PublishAutomationRequest")


@_attrs_define
class PublishAutomationRequest:
    """Saved draft revision to publish, with explicit YAML takeover when needed."""

    expected_version: int
    """Current saved draft revision to validate and publish."""
    take_over_manifest: bool | Unset = False

    def to_dict(self) -> dict[str, Any]:
        expected_version = self.expected_version

        take_over_manifest = self.take_over_manifest

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "expected_version": expected_version,
            }
        )
        if take_over_manifest is not UNSET:
            field_dict["take_over_manifest"] = take_over_manifest

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        expected_version = d.pop("expected_version")

        take_over_manifest = d.pop("take_over_manifest", UNSET)

        publish_automation_request = cls(
            expected_version=expected_version,
            take_over_manifest=take_over_manifest,
        )

        return publish_automation_request
