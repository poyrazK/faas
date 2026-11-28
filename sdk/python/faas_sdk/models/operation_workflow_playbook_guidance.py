from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.operation_workflow_playbook import OperationWorkflowPlaybook


T = TypeVar("T", bound="OperationWorkflowPlaybookGuidance")


@_attrs_define
class OperationWorkflowPlaybookGuidance:
    """OperationWorkflowPlaybookGuidance. Application-owned advisory guidance; evidence requirements are human checklists
    and links are never executed by Gregale.

    """

    playbook: OperationWorkflowPlaybook
    """OperationWorkflowPlaybook. Application-owned advisory guidance; evidence requirements are human checklists
    and links are never executed by Gregale."""
    contract_version: int
    """OperationWorkflowPlaybookGuidance.contract_version. Workflow contract version of the retained current
    report."""
    reporting_operation_id: UUID
    """OperationWorkflowPlaybookGuidance.reporting_operation_id. Operation whose immutable definition supplied this
    playbook."""

    def to_dict(self) -> dict[str, Any]:
        playbook = self.playbook.to_dict()

        contract_version = self.contract_version

        reporting_operation_id = str(self.reporting_operation_id)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "playbook": playbook,
                "contract_version": contract_version,
                "reporting_operation_id": reporting_operation_id,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_playbook import OperationWorkflowPlaybook

        d = dict(src_dict)
        playbook = OperationWorkflowPlaybook.from_dict(d.pop("playbook"))

        contract_version = d.pop("contract_version")

        reporting_operation_id = UUID(d.pop("reporting_operation_id"))

        operation_workflow_playbook_guidance = cls(
            playbook=playbook,
            contract_version=contract_version,
            reporting_operation_id=reporting_operation_id,
        )

        return operation_workflow_playbook_guidance
