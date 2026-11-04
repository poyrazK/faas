from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_business_condition_result import OperationBusinessConditionResult


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
    conditions: list[OperationBusinessConditionResult] | Unset = UNSET
    """Optional unique condition IDs declared by the pinned milestone step. Canonical order is by ID; existing
    decisions omit this field."""

    def to_dict(self) -> dict[str, Any]:
        workflow = self.workflow

        instance_id = self.instance_id

        code = self.code

        description = self.description

        rule_id = self.rule_id

        rule_version = self.rule_version

        conditions: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.conditions, Unset):
            conditions = []
            for conditions_item_data in self.conditions:
                conditions_item = conditions_item_data.to_dict()
                conditions.append(conditions_item)

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
        if conditions is not UNSET:
            field_dict["conditions"] = conditions

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_business_condition_result import OperationBusinessConditionResult

        d = dict(src_dict)
        workflow = d.pop("workflow")

        instance_id = d.pop("instance_id")

        code = d.pop("code")

        description = d.pop("description")

        rule_id = d.pop("rule_id")

        rule_version = d.pop("rule_version")

        _conditions = d.pop("conditions", UNSET)
        conditions: list[OperationBusinessConditionResult] | Unset = UNSET
        if _conditions is not UNSET:
            conditions = []
            for conditions_item_data in _conditions:
                conditions_item = OperationBusinessConditionResult.from_dict(conditions_item_data)

                conditions.append(conditions_item)

        operation_business_decision = cls(
            workflow=workflow,
            instance_id=instance_id,
            code=code,
            description=description,
            rule_id=rule_id,
            rule_version=rule_version,
            conditions=conditions,
        )

        return operation_business_decision
