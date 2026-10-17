from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="RunAutomationTestsRequest")


@_attrs_define
class RunAutomationTestsRequest:
    """Expected current draft version for simulating its saved test cases."""

    expected_version: int

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

        run_automation_tests_request = cls(
            expected_version=expected_version,
        )

        return run_automation_tests_request
