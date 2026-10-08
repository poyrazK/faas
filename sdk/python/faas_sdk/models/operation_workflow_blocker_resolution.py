from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="OperationWorkflowBlockerResolution")


@_attrs_define
class OperationWorkflowBlockerResolution:
    """Explicit application explanation for clearing one prior blocker occurrence. The source must be a retained report in
    the same owner/business-reference/workflow-instance/contract-version boundary and must contain this target/code. The
    containing state report supplies the resolution identity and timestamps. A cleared list alone does not imply a
    resolution fact.

    """

    code: str
    operation: str
    description: str
    """Public UTF-8 explanation limited to 512 bytes without control characters."""
    blocker_operation_id: UUID
    blocker_report_id: UUID
    blocker_revision: int
    """Source revision must precede the resolution report revision."""

    def to_dict(self) -> dict[str, Any]:
        code = self.code

        operation = self.operation

        description = self.description

        blocker_operation_id = str(self.blocker_operation_id)

        blocker_report_id = str(self.blocker_report_id)

        blocker_revision = self.blocker_revision

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "code": code,
                "operation": operation,
                "description": description,
                "blocker_operation_id": blocker_operation_id,
                "blocker_report_id": blocker_report_id,
                "blocker_revision": blocker_revision,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        code = d.pop("code")

        operation = d.pop("operation")

        description = d.pop("description")

        blocker_operation_id = UUID(d.pop("blocker_operation_id"))

        blocker_report_id = UUID(d.pop("blocker_report_id"))

        blocker_revision = d.pop("blocker_revision")

        operation_workflow_blocker_resolution = cls(
            code=code,
            operation=operation,
            description=description,
            blocker_operation_id=blocker_operation_id,
            blocker_report_id=blocker_report_id,
            blocker_revision=blocker_revision,
        )

        return operation_workflow_blocker_resolution
