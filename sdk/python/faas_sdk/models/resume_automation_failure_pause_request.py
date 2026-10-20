from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="ResumeAutomationFailurePauseRequest")


@_attrs_define
class ResumeAutomationFailurePauseRequest:
    """Explicit failure guard resume guarded by its current generation."""

    expected_generation: int
    """Positive pause generation reviewed by the caller."""

    def to_dict(self) -> dict[str, Any]:
        expected_generation = self.expected_generation

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "expected_generation": expected_generation,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        expected_generation = d.pop("expected_generation")

        resume_automation_failure_pause_request = cls(
            expected_generation=expected_generation,
        )

        return resume_automation_failure_pause_request
