from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="RestoreAutomationRevisionRequest")


@_attrs_define
class RestoreAutomationRevisionRequest:
    """Restores a selected immutable publication into the draft without publishing it."""

    expected_version: int
    """Current draft revision for optimistic concurrency; zero creates a draft after deletion."""

    def to_dict(self) -> dict[str, Any]:
        expected_version = self.expected_version

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "expected_version": expected_version,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        expected_version = d.pop("expected_version")

        restore_automation_revision_request = cls(
            expected_version=expected_version,
        )

        return restore_automation_revision_request
