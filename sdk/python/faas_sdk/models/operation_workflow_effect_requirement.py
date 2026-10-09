from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="OperationWorkflowEffectRequirement")


@_attrs_define
class OperationWorkflowEffectRequirement:
    """Exact effect code and version that must be confirmed by fresh transition evidence."""

    milestone: str
    code: str
    version: str

    def to_dict(self) -> dict[str, Any]:
        milestone = self.milestone

        code = self.code

        version = self.version

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "milestone": milestone,
                "code": code,
                "version": version,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        milestone = d.pop("milestone")

        code = d.pop("code")

        version = d.pop("version")

        operation_workflow_effect_requirement = cls(
            milestone=milestone,
            code=code,
            version=version,
        )

        return operation_workflow_effect_requirement
