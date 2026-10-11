from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_performance_group_dimension import (
    OperationWorkflowPerformanceGroupDimension,
    check_operation_workflow_performance_group_dimension,
)
from ..models.operation_workflow_performance_group_kind import (
    OperationWorkflowPerformanceGroupKind,
    check_operation_workflow_performance_group_kind,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationWorkflowPerformanceGroup")


@_attrs_define
class OperationWorkflowPerformanceGroup:
    """Exact duration dimension and optional contract, state, blocker or owner selectors for contributor reads."""

    dimension: OperationWorkflowPerformanceGroupDimension
    kind: OperationWorkflowPerformanceGroupKind | Unset = UNSET
    decision_code: str | Unset = UNSET
    rule_id: str | Unset = UNSET
    rule_version: str | Unset = UNSET
    group_id: str | Unset = UNSET
    path_id: str | Unset = UNSET
    outcome_code: str | Unset = UNSET
    contract_version: int | Unset = UNSET
    state: str | Unset = UNSET
    operation: str | Unset = UNSET
    code: str | Unset = UNSET
    owner: str | Unset = UNSET
    unassigned: bool | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        dimension: str = self.dimension

        kind: str | Unset = UNSET
        if not isinstance(self.kind, Unset):
            kind = self.kind

        decision_code = self.decision_code

        rule_id = self.rule_id

        rule_version = self.rule_version

        group_id = self.group_id

        path_id = self.path_id

        outcome_code = self.outcome_code

        contract_version = self.contract_version

        state = self.state

        operation = self.operation

        code = self.code

        owner = self.owner

        unassigned = self.unassigned

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "dimension": dimension,
            }
        )
        if kind is not UNSET:
            field_dict["kind"] = kind
        if decision_code is not UNSET:
            field_dict["decision_code"] = decision_code
        if rule_id is not UNSET:
            field_dict["rule_id"] = rule_id
        if rule_version is not UNSET:
            field_dict["rule_version"] = rule_version
        if group_id is not UNSET:
            field_dict["group_id"] = group_id
        if path_id is not UNSET:
            field_dict["path_id"] = path_id
        if outcome_code is not UNSET:
            field_dict["outcome_code"] = outcome_code
        if contract_version is not UNSET:
            field_dict["contract_version"] = contract_version
        if state is not UNSET:
            field_dict["state"] = state
        if operation is not UNSET:
            field_dict["operation"] = operation
        if code is not UNSET:
            field_dict["code"] = code
        if owner is not UNSET:
            field_dict["owner"] = owner
        if unassigned is not UNSET:
            field_dict["unassigned"] = unassigned

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        dimension = check_operation_workflow_performance_group_dimension(d.pop("dimension"))

        _kind = d.pop("kind", UNSET)
        kind: OperationWorkflowPerformanceGroupKind | Unset
        if isinstance(_kind, Unset):
            kind = UNSET
        else:
            kind = check_operation_workflow_performance_group_kind(_kind)

        decision_code = d.pop("decision_code", UNSET)

        rule_id = d.pop("rule_id", UNSET)

        rule_version = d.pop("rule_version", UNSET)

        group_id = d.pop("group_id", UNSET)

        path_id = d.pop("path_id", UNSET)

        outcome_code = d.pop("outcome_code", UNSET)

        contract_version = d.pop("contract_version", UNSET)

        state = d.pop("state", UNSET)

        operation = d.pop("operation", UNSET)

        code = d.pop("code", UNSET)

        owner = d.pop("owner", UNSET)

        unassigned = d.pop("unassigned", UNSET)

        operation_workflow_performance_group = cls(
            dimension=dimension,
            kind=kind,
            decision_code=decision_code,
            rule_id=rule_id,
            rule_version=rule_version,
            group_id=group_id,
            path_id=path_id,
            outcome_code=outcome_code,
            contract_version=contract_version,
            state=state,
            operation=operation,
            code=code,
            owner=owner,
            unassigned=unassigned,
        )

        return operation_workflow_performance_group
