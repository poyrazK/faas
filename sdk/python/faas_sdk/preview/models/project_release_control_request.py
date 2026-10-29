from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="ProjectReleaseControlRequest")


@_attrs_define
class ProjectReleaseControlRequest:
    """An operator decision fenced against the inspected progressive checkpoint."""

    request_id: str
    """Reuse this identity after an ambiguous response."""
    expected_generation: int
    """Inspected checkpoint generation."""

    def to_dict(self) -> dict[str, Any]:
        request_id = self.request_id

        expected_generation = self.expected_generation

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "request_id": request_id,
                "expected_generation": expected_generation,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        request_id = d.pop("request_id")

        expected_generation = d.pop("expected_generation")

        project_release_control_request = cls(
            request_id=request_id,
            expected_generation=expected_generation,
        )

        return project_release_control_request
