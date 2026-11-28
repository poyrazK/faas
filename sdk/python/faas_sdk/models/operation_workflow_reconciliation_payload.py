from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_reconciliation_payload_kind import (
    OperationWorkflowReconciliationPayloadKind,
    check_operation_workflow_reconciliation_payload_kind,
)

if TYPE_CHECKING:
    from ..models.operation_workflow_reconciliation import OperationWorkflowReconciliation


T = TypeVar("T", bound="OperationWorkflowReconciliationPayload")


@_attrs_define
class OperationWorkflowReconciliationPayload:
    """Application-reported discrepancy recorded as a declared milestone using the existing transaction, ownership, and
    retention boundaries.

    """

    kind: OperationWorkflowReconciliationPayloadKind
    reconciliation: OperationWorkflowReconciliation
    """Application comparison of authoritative business state and revision against the retained workflow
    projection."""

    def to_dict(self) -> dict[str, Any]:
        kind: str = self.kind

        reconciliation = self.reconciliation.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "kind": kind,
                "reconciliation": reconciliation,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_reconciliation import OperationWorkflowReconciliation

        d = dict(src_dict)
        kind = check_operation_workflow_reconciliation_payload_kind(d.pop("kind"))

        reconciliation = OperationWorkflowReconciliation.from_dict(d.pop("reconciliation"))

        operation_workflow_reconciliation_payload = cls(
            kind=kind,
            reconciliation=reconciliation,
        )

        return operation_workflow_reconciliation_payload
