from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="OperationWorkflowPlaybookProgressStartRequest")


@_attrs_define
class OperationWorkflowPlaybookProgressStartRequest:
    """OperationWorkflowPlaybookProgressStartRequest. Durable advisory checklist coordination, independent of application
    business success.

    """

    playbook_id: str
    """OperationWorkflowPlaybookProgressStartRequest.playbook_id. Exact matched rule identifier."""
    playbook_version: int
    """OperationWorkflowPlaybookProgressStartRequest.playbook_version. Pinned rule revision."""
    contract_version: int
    """OperationWorkflowPlaybookProgressStartRequest.contract_version. Pinned workflow contract version."""
    reporting_operation_id: UUID
    """OperationWorkflowPlaybookProgressStartRequest.reporting_operation_id. Exact operation reporting the matched
    current guidance."""

    def to_dict(self) -> dict[str, Any]:
        playbook_id = self.playbook_id

        playbook_version = self.playbook_version

        contract_version = self.contract_version

        reporting_operation_id = str(self.reporting_operation_id)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "playbook_id": playbook_id,
                "playbook_version": playbook_version,
                "contract_version": contract_version,
                "reporting_operation_id": reporting_operation_id,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        playbook_id = d.pop("playbook_id")

        playbook_version = d.pop("playbook_version")

        contract_version = d.pop("contract_version")

        reporting_operation_id = UUID(d.pop("reporting_operation_id"))

        operation_workflow_playbook_progress_start_request = cls(
            playbook_id=playbook_id,
            playbook_version=playbook_version,
            contract_version=contract_version,
            reporting_operation_id=reporting_operation_id,
        )

        return operation_workflow_playbook_progress_start_request
