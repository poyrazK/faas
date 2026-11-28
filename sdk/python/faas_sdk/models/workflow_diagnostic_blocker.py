from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.workflow_diagnostic_blocker_code import (
    WorkflowDiagnosticBlockerCode,
    check_workflow_diagnostic_blocker_code,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="WorkflowDiagnosticBlocker")


@_attrs_define
class WorkflowDiagnosticBlocker:
    """A stable recovery reason with a fixed explanation and optional affected step."""

    code: WorkflowDiagnosticBlockerCode
    message: str
    """Fixed explanation without private values."""
    step_name: str | Unset = UNSET
    """Affected step when the planner blocker is step-specific."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        code: str = self.code

        message = self.message

        step_name = self.step_name

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "code": code,
                "message": message,
            }
        )
        if step_name is not UNSET:
            field_dict["step_name"] = step_name

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        code = check_workflow_diagnostic_blocker_code(d.pop("code"))

        message = d.pop("message")

        step_name = d.pop("step_name", UNSET)

        workflow_diagnostic_blocker = cls(
            code=code,
            message=message,
            step_name=step_name,
        )

        workflow_diagnostic_blocker.additional_properties = d
        return workflow_diagnostic_blocker

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> Any:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: Any) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
