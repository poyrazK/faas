from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationWorkflowConditionEvidence")


@_attrs_define
class OperationWorkflowConditionEvidence:
    """Exact retained report and decision fact reference with reported rule identity. Previous evidence is absent when the
    baseline did not report a decision in this context. Links refer to the current retained Operation view.

    """

    report_id: UUID
    operation_id: UUID
    revision: int
    milestone_id: UUID
    decision_code: str
    rule_id: str
    rule_version: str
    path_id: str | Unset = UNSET
    """Selected path when the decision was reported for a path group."""

    def to_dict(self) -> dict[str, Any]:
        report_id = str(self.report_id)

        operation_id = str(self.operation_id)

        revision = self.revision

        milestone_id = str(self.milestone_id)

        decision_code = self.decision_code

        rule_id = self.rule_id

        rule_version = self.rule_version

        path_id = self.path_id

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "report_id": report_id,
                "operation_id": operation_id,
                "revision": revision,
                "milestone_id": milestone_id,
                "decision_code": decision_code,
                "rule_id": rule_id,
                "rule_version": rule_version,
            }
        )
        if path_id is not UNSET:
            field_dict["path_id"] = path_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        report_id = UUID(d.pop("report_id"))

        operation_id = UUID(d.pop("operation_id"))

        revision = d.pop("revision")

        milestone_id = UUID(d.pop("milestone_id"))

        decision_code = d.pop("decision_code")

        rule_id = d.pop("rule_id")

        rule_version = d.pop("rule_version")

        path_id = d.pop("path_id", UNSET)

        operation_workflow_condition_evidence = cls(
            report_id=report_id,
            operation_id=operation_id,
            revision=revision,
            milestone_id=milestone_id,
            decision_code=decision_code,
            rule_id=rule_id,
            rule_version=rule_version,
            path_id=path_id,
        )

        return operation_workflow_condition_evidence
