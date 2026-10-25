from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_business_condition_result import OperationBusinessConditionResult


T = TypeVar("T", bound="OperationWorkflowPathSelection")


@_attrs_define
class OperationWorkflowPathSelection:
    """Application-reported choice of one named path in a compound prerequisite group of the selected pinned transition.
    Each group may appear once. Choices do not authorize or execute actions.

    """

    group_id: str
    path_id: str
    decision_conditions: list[OperationBusinessConditionResult] | Unset = UNSET
    """Optional application condition snapshot; requires a decision fact reference and exact equality with that
    fact. Omitted for legacy decisions."""
    decision_milestone_id: UUID | Unset = UNSET
    """Optional business decision milestone in this report evidence list and application transaction. When present
    all decision fields are required and must match its payload."""
    decision_code: str | Unset = UNSET
    decision_description: str | Unset = UNSET
    decision_rule_id: str | Unset = UNSET
    decision_rule_version: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        group_id = self.group_id

        path_id = self.path_id

        decision_conditions: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.decision_conditions, Unset):
            decision_conditions = []
            for decision_conditions_item_data in self.decision_conditions:
                decision_conditions_item = decision_conditions_item_data.to_dict()
                decision_conditions.append(decision_conditions_item)

        decision_milestone_id: str | Unset = UNSET
        if not isinstance(self.decision_milestone_id, Unset):
            decision_milestone_id = str(self.decision_milestone_id)

        decision_code = self.decision_code

        decision_description = self.decision_description

        decision_rule_id = self.decision_rule_id

        decision_rule_version = self.decision_rule_version

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "group_id": group_id,
                "path_id": path_id,
            }
        )
        if decision_conditions is not UNSET:
            field_dict["decision_conditions"] = decision_conditions
        if decision_milestone_id is not UNSET:
            field_dict["decision_milestone_id"] = decision_milestone_id
        if decision_code is not UNSET:
            field_dict["decision_code"] = decision_code
        if decision_description is not UNSET:
            field_dict["decision_description"] = decision_description
        if decision_rule_id is not UNSET:
            field_dict["decision_rule_id"] = decision_rule_id
        if decision_rule_version is not UNSET:
            field_dict["decision_rule_version"] = decision_rule_version

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_business_condition_result import OperationBusinessConditionResult

        d = dict(src_dict)
        group_id = d.pop("group_id")

        path_id = d.pop("path_id")

        _decision_conditions = d.pop("decision_conditions", UNSET)
        decision_conditions: list[OperationBusinessConditionResult] | Unset = UNSET
        if _decision_conditions is not UNSET:
            decision_conditions = []
            for decision_conditions_item_data in _decision_conditions:
                decision_conditions_item = OperationBusinessConditionResult.from_dict(decision_conditions_item_data)

                decision_conditions.append(decision_conditions_item)

        _decision_milestone_id = d.pop("decision_milestone_id", UNSET)
        decision_milestone_id: UUID | Unset
        if isinstance(_decision_milestone_id, Unset):
            decision_milestone_id = UNSET
        else:
            decision_milestone_id = UUID(_decision_milestone_id)

        decision_code = d.pop("decision_code", UNSET)

        decision_description = d.pop("decision_description", UNSET)

        decision_rule_id = d.pop("decision_rule_id", UNSET)

        decision_rule_version = d.pop("decision_rule_version", UNSET)

        operation_workflow_path_selection = cls(
            group_id=group_id,
            path_id=path_id,
            decision_conditions=decision_conditions,
            decision_milestone_id=decision_milestone_id,
            decision_code=decision_code,
            decision_description=decision_description,
            decision_rule_id=decision_rule_id,
            decision_rule_version=decision_rule_version,
        )

        return operation_workflow_path_selection
