from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_performance_group_dimension import (
    OperationWorkflowPerformanceGroupDimension,
    check_operation_workflow_performance_group_dimension,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationWorkflowPerformanceGroup")


@_attrs_define
class OperationWorkflowPerformanceGroup:
    """Exact duration dimension and optional contract, state, blocker or owner selectors for contributor reads."""

    dimension: OperationWorkflowPerformanceGroupDimension
    contract_version: int | Unset = UNSET
    state: str | Unset = UNSET
    operation: str | Unset = UNSET
    code: str | Unset = UNSET
    owner: str | Unset = UNSET
    unassigned: bool | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        dimension: str = self.dimension

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

        contract_version = d.pop("contract_version", UNSET)

        state = d.pop("state", UNSET)

        operation = d.pop("operation", UNSET)

        code = d.pop("code", UNSET)

        owner = d.pop("owner", UNSET)

        unassigned = d.pop("unassigned", UNSET)

        operation_workflow_performance_group = cls(
            dimension=dimension,
            contract_version=contract_version,
            state=state,
            operation=operation,
            code=code,
            owner=owner,
            unassigned=unassigned,
        )

        return operation_workflow_performance_group
