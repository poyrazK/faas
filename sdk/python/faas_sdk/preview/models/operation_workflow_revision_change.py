from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_revision_change_kind import (
    OperationWorkflowRevisionChangeKind,
    check_operation_workflow_revision_change_kind,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationWorkflowRevisionChange")


@_attrs_define
class OperationWorkflowRevisionChange:
    """Deterministic reported state or blocker or path or decision change. Before and after contain bounded public snapshot
    text; removal of a blocker does not prove business resolution.

    """

    kind: OperationWorkflowRevisionChangeKind
    key: str
    explanation: str
    before: str | Unset = UNSET
    after: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        kind: str = self.kind

        key = self.key

        explanation = self.explanation

        before = self.before

        after = self.after

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "kind": kind,
                "key": key,
                "explanation": explanation,
            }
        )
        if before is not UNSET:
            field_dict["before"] = before
        if after is not UNSET:
            field_dict["after"] = after

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        kind = check_operation_workflow_revision_change_kind(d.pop("kind"))

        key = d.pop("key")

        explanation = d.pop("explanation")

        before = d.pop("before", UNSET)

        after = d.pop("after", UNSET)

        operation_workflow_revision_change = cls(
            kind=kind,
            key=key,
            explanation=explanation,
            before=before,
            after=after,
        )

        return operation_workflow_revision_change
