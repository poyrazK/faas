from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_dependency_finding_kind import (
    OperationWorkflowDependencyFindingKind,
    check_operation_workflow_dependency_finding_kind,
)
from ..models.operation_workflow_dependency_finding_limit import (
    OperationWorkflowDependencyFindingLimit,
    check_operation_workflow_dependency_finding_limit,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_dependency import OperationWorkflowDependency
    from ..models.operation_workflow_state import OperationWorkflowState


T = TypeVar("T", bound="OperationWorkflowDependencyFinding")


@_attrs_define
class OperationWorkflowDependencyFinding:
    """Observed prerequisite-chain finding with a representative path starting at the selected workflow. Shared workflows
    are expanded once; this is not an exhaustive enumeration of every route or execution authorization.

    """

    kind: OperationWorkflowDependencyFindingKind
    path: list[OperationWorkflowDependency]
    explanation: str
    state: OperationWorkflowState | Unset = UNSET
    """Latest app-reported state for one declared workflow instance, including terminal and staleness indicators."""
    limit: OperationWorkflowDependencyFindingLimit | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        kind: str = self.kind

        path = []
        for path_item_data in self.path:
            path_item = path_item_data.to_dict()
            path.append(path_item)

        explanation = self.explanation

        state: dict[str, Any] | Unset = UNSET
        if not isinstance(self.state, Unset):
            state = self.state.to_dict()

        limit: str | Unset = UNSET
        if not isinstance(self.limit, Unset):
            limit = self.limit

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "kind": kind,
                "path": path,
                "explanation": explanation,
            }
        )
        if state is not UNSET:
            field_dict["state"] = state
        if limit is not UNSET:
            field_dict["limit"] = limit

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_dependency import OperationWorkflowDependency
        from ..models.operation_workflow_state import OperationWorkflowState

        d = dict(src_dict)
        kind = check_operation_workflow_dependency_finding_kind(d.pop("kind"))

        path = []
        _path = d.pop("path")
        for path_item_data in _path:
            path_item = OperationWorkflowDependency.from_dict(path_item_data)

            path.append(path_item)

        explanation = d.pop("explanation")

        _state = d.pop("state", UNSET)
        state: OperationWorkflowState | Unset
        if isinstance(_state, Unset):
            state = UNSET
        else:
            state = OperationWorkflowState.from_dict(_state)

        _limit = d.pop("limit", UNSET)
        limit: OperationWorkflowDependencyFindingLimit | Unset
        if isinstance(_limit, Unset):
            limit = UNSET
        else:
            limit = check_operation_workflow_dependency_finding_limit(_limit)

        operation_workflow_dependency_finding = cls(
            kind=kind,
            path=path,
            explanation=explanation,
            state=state,
            limit=limit,
        )

        return operation_workflow_dependency_finding
