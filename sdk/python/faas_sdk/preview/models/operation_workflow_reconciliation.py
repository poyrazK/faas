from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_reconciliation_status import (
    OperationWorkflowReconciliationStatus,
    check_operation_workflow_reconciliation_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationWorkflowReconciliation")


@_attrs_define
class OperationWorkflowReconciliation:
    """Application comparison of authoritative business state and revision against the retained workflow projection."""

    workflow: str
    instance_id: str
    authoritative_state: str
    source_revision: str
    """Opaque application business revision; distinct from the SDK report counter. UTF-8 byte bound."""
    expected_report_revision: int
    contract_version: int
    observed_report_revision: int
    observed_contract_version: int
    status: OperationWorkflowReconciliationStatus
    observed_state: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        workflow = self.workflow

        instance_id = self.instance_id

        authoritative_state = self.authoritative_state

        source_revision = self.source_revision

        expected_report_revision = self.expected_report_revision

        contract_version = self.contract_version

        observed_report_revision = self.observed_report_revision

        observed_contract_version = self.observed_contract_version

        status: str = self.status

        observed_state = self.observed_state

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "workflow": workflow,
                "instance_id": instance_id,
                "authoritative_state": authoritative_state,
                "source_revision": source_revision,
                "expected_report_revision": expected_report_revision,
                "contract_version": contract_version,
                "observed_report_revision": observed_report_revision,
                "observed_contract_version": observed_contract_version,
                "status": status,
            }
        )
        if observed_state is not UNSET:
            field_dict["observed_state"] = observed_state

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        workflow = d.pop("workflow")

        instance_id = d.pop("instance_id")

        authoritative_state = d.pop("authoritative_state")

        source_revision = d.pop("source_revision")

        expected_report_revision = d.pop("expected_report_revision")

        contract_version = d.pop("contract_version")

        observed_report_revision = d.pop("observed_report_revision")

        observed_contract_version = d.pop("observed_contract_version")

        status = check_operation_workflow_reconciliation_status(d.pop("status"))

        observed_state = d.pop("observed_state", UNSET)

        operation_workflow_reconciliation = cls(
            workflow=workflow,
            instance_id=instance_id,
            authoritative_state=authoritative_state,
            source_revision=source_revision,
            expected_report_revision=expected_report_revision,
            contract_version=contract_version,
            observed_report_revision=observed_report_revision,
            observed_contract_version=observed_contract_version,
            status=status,
            observed_state=observed_state,
        )

        return operation_workflow_reconciliation
