from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.control_data_pipeline_request_action import (
    ControlDataPipelineRequestAction,
    check_control_data_pipeline_request_action,
)

T = TypeVar("T", bound="ControlDataPipelineRequest")


@_attrs_define
class ControlDataPipelineRequest:
    """Optimistic revision fence and requested pipeline lifecycle action."""

    expected_revision: int
    action: ControlDataPipelineRequestAction

    def to_dict(self) -> dict[str, Any]:
        expected_revision = self.expected_revision

        action: str = self.action

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "expected_revision": expected_revision,
                "action": action,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        expected_revision = d.pop("expected_revision")

        action = check_control_data_pipeline_request_action(d.pop("action"))

        control_data_pipeline_request = cls(
            expected_revision=expected_revision,
            action=action,
        )

        return control_data_pipeline_request
