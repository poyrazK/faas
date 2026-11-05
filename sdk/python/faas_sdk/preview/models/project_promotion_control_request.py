from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="ProjectPromotionControlRequest")


@_attrs_define
class ProjectPromotionControlRequest:
    """Stable operator request identity and expected promotion checkpoint generation."""

    request_id: str
    expected_generation: int

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

        project_promotion_control_request = cls(
            request_id=request_id,
            expected_generation=expected_generation,
        )

        return project_promotion_control_request
