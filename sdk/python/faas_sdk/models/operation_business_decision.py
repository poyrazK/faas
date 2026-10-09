from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="OperationBusinessDecision")


@_attrs_define
class OperationBusinessDecision:
    """Application-reported business decision with a public explanation and optional exact policy rule identity."""

    workflow: str
    instance_id: str
    """UTF-8 byte limit; nonempty text without control characters."""
    code: str
    description: str
    """Public decision explanation; UTF-8 byte limit; nonempty text without control characters."""
    rule_id: str
    rule_version: str
    """Exact policy rule version; UTF-8 byte limit; nonempty text without control characters."""

    def to_dict(self) -> dict[str, Any]:
        workflow = self.workflow

        instance_id = self.instance_id

        code = self.code

        description = self.description

        rule_id = self.rule_id

        rule_version = self.rule_version

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "workflow": workflow,
                "instance_id": instance_id,
                "code": code,
                "description": description,
                "rule_id": rule_id,
                "rule_version": rule_version,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        workflow = d.pop("workflow")

        instance_id = d.pop("instance_id")

        code = d.pop("code")

        description = d.pop("description")

        rule_id = d.pop("rule_id")

        rule_version = d.pop("rule_version")

        operation_business_decision = cls(
            workflow=workflow,
            instance_id=instance_id,
            code=code,
            description=description,
            rule_id=rule_id,
            rule_version=rule_version,
        )

        return operation_business_decision
