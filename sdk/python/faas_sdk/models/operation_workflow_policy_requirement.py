from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="OperationWorkflowPolicyRequirement")


@_attrs_define
class OperationWorkflowPolicyRequirement:
    milestone: str
    rule_id: str
    rule_version: str
    code: str

    def to_dict(self) -> dict[str, Any]:
        milestone = self.milestone

        rule_id = self.rule_id

        rule_version = self.rule_version

        code = self.code

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "milestone": milestone,
                "rule_id": rule_id,
                "rule_version": rule_version,
                "code": code,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        milestone = d.pop("milestone")

        rule_id = d.pop("rule_id")

        rule_version = d.pop("rule_version")

        code = d.pop("code")

        operation_workflow_policy_requirement = cls(
            milestone=milestone,
            rule_id=rule_id,
            rule_version=rule_version,
            code=code,
        )

        return operation_workflow_policy_requirement
