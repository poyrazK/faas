from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.operation_definition_summary_http_transaction_version import (
    OperationDefinitionSummaryHttpTransactionVersion,
    check_operation_definition_summary_http_transaction_version,
)
from ..models.operation_definition_summary_method import (
    OperationDefinitionSummaryMethod,
    check_operation_definition_summary_method,
)
from ..models.operation_definition_summary_owner import (
    OperationDefinitionSummaryOwner,
    check_operation_definition_summary_owner,
)
from ..models.operation_definition_summary_recovery import (
    OperationDefinitionSummaryRecovery,
    check_operation_definition_summary_recovery,
)
from ..models.operation_definition_summary_transaction_receipt import (
    OperationDefinitionSummaryTransactionReceipt,
    check_operation_definition_summary_transaction_receipt,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_subject_spec import OperationSubjectSpec
    from ..models.operation_workflow_step import OperationWorkflowStep


T = TypeVar("T", bound="OperationDefinitionSummary")


@_attrs_define
class OperationDefinitionSummary:
    """Immutable definition metadata without bundled schema documents."""

    id: UUID
    app_id: UUID
    scope: str
    revision: str
    deployment_id: UUID
    name: str
    method: OperationDefinitionSummaryMethod
    path: str
    owner: OperationDefinitionSummaryOwner
    recovery: OperationDefinitionSummaryRecovery
    progress_stages: list[str]
    created_at: datetime.datetime
    milestones: list[str] | Unset = UNSET
    """Declared milestone names; schema documents are available on the full definition."""
    workflow_steps: list[OperationWorkflowStep] | Unset = UNSET
    """Resolved app-declared read-only workflow steps mapped to this definition's transaction-backed milestones."""
    subject: OperationSubjectSpec | Unset = UNSET
    """Optional public business reference extracted once from validated input on new admission. This metadata does
    not authorize access to the business entity."""
    http_transaction_version: OperationDefinitionSummaryHttpTransactionVersion | Unset = UNSET
    """Opt-in customer-owned HTTP transaction protocol. Absence means ordinary HTTP execution."""
    release_id: UUID | Unset = UNSET
    job: str | Unset = UNSET
    """Discovered account-owned single-task batch Job; accepted work retains its execution snapshot."""
    workflow: str | Unset = UNSET
    """Workflow execution selected by this discoverable Operations definition."""
    transaction_receipt: OperationDefinitionSummaryTransactionReceipt | Unset = UNSET
    """Discovered customer transaction receipt contract for atomic HTTP completion."""
    completion_webhook_id: UUID | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        app_id = str(self.app_id)

        scope = self.scope

        revision = self.revision

        deployment_id = str(self.deployment_id)

        name = self.name

        method: str = self.method

        path = self.path

        owner: str = self.owner

        recovery: str = self.recovery

        progress_stages = self.progress_stages

        created_at = self.created_at.isoformat()

        milestones: list[str] | Unset = UNSET
        if not isinstance(self.milestones, Unset):
            milestones = self.milestones

        workflow_steps: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.workflow_steps, Unset):
            workflow_steps = []
            for workflow_steps_item_data in self.workflow_steps:
                workflow_steps_item = workflow_steps_item_data.to_dict()
                workflow_steps.append(workflow_steps_item)

        subject: dict[str, Any] | Unset = UNSET
        if not isinstance(self.subject, Unset):
            subject = self.subject.to_dict()

        http_transaction_version: int | Unset = UNSET
        if not isinstance(self.http_transaction_version, Unset):
            http_transaction_version = self.http_transaction_version

        release_id: str | Unset = UNSET
        if not isinstance(self.release_id, Unset):
            release_id = str(self.release_id)

        job = self.job

        workflow = self.workflow

        transaction_receipt: str | Unset = UNSET
        if not isinstance(self.transaction_receipt, Unset):
            transaction_receipt = self.transaction_receipt

        completion_webhook_id: str | Unset = UNSET
        if not isinstance(self.completion_webhook_id, Unset):
            completion_webhook_id = str(self.completion_webhook_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "app_id": app_id,
                "scope": scope,
                "revision": revision,
                "deployment_id": deployment_id,
                "name": name,
                "method": method,
                "path": path,
                "owner": owner,
                "recovery": recovery,
                "progress_stages": progress_stages,
                "created_at": created_at,
            }
        )
        if milestones is not UNSET:
            field_dict["milestones"] = milestones
        if workflow_steps is not UNSET:
            field_dict["workflow_steps"] = workflow_steps
        if subject is not UNSET:
            field_dict["subject"] = subject
        if http_transaction_version is not UNSET:
            field_dict["http_transaction_version"] = http_transaction_version
        if release_id is not UNSET:
            field_dict["release_id"] = release_id
        if job is not UNSET:
            field_dict["job"] = job
        if workflow is not UNSET:
            field_dict["workflow"] = workflow
        if transaction_receipt is not UNSET:
            field_dict["transaction_receipt"] = transaction_receipt
        if completion_webhook_id is not UNSET:
            field_dict["completion_webhook_id"] = completion_webhook_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_subject_spec import OperationSubjectSpec
        from ..models.operation_workflow_step import OperationWorkflowStep

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        app_id = UUID(d.pop("app_id"))

        scope = d.pop("scope")

        revision = d.pop("revision")

        deployment_id = UUID(d.pop("deployment_id"))

        name = d.pop("name")

        method = check_operation_definition_summary_method(d.pop("method"))

        path = d.pop("path")

        owner = check_operation_definition_summary_owner(d.pop("owner"))

        recovery = check_operation_definition_summary_recovery(d.pop("recovery"))

        progress_stages = cast(list[str], d.pop("progress_stages"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        milestones = cast(list[str], d.pop("milestones", UNSET))

        _workflow_steps = d.pop("workflow_steps", UNSET)
        workflow_steps: list[OperationWorkflowStep] | Unset = UNSET
        if _workflow_steps is not UNSET:
            workflow_steps = []
            for workflow_steps_item_data in _workflow_steps:
                workflow_steps_item = OperationWorkflowStep.from_dict(workflow_steps_item_data)

                workflow_steps.append(workflow_steps_item)

        _subject = d.pop("subject", UNSET)
        subject: OperationSubjectSpec | Unset
        if isinstance(_subject, Unset):
            subject = UNSET
        else:
            subject = OperationSubjectSpec.from_dict(_subject)

        _http_transaction_version = d.pop("http_transaction_version", UNSET)
        http_transaction_version: OperationDefinitionSummaryHttpTransactionVersion | Unset
        if isinstance(_http_transaction_version, Unset):
            http_transaction_version = UNSET
        else:
            http_transaction_version = check_operation_definition_summary_http_transaction_version(
                _http_transaction_version
            )

        _release_id = d.pop("release_id", UNSET)
        release_id: UUID | Unset
        if isinstance(_release_id, Unset):
            release_id = UNSET
        else:
            release_id = UUID(_release_id)

        job = d.pop("job", UNSET)

        workflow = d.pop("workflow", UNSET)

        _transaction_receipt = d.pop("transaction_receipt", UNSET)
        transaction_receipt: OperationDefinitionSummaryTransactionReceipt | Unset
        if isinstance(_transaction_receipt, Unset):
            transaction_receipt = UNSET
        else:
            transaction_receipt = check_operation_definition_summary_transaction_receipt(_transaction_receipt)

        _completion_webhook_id = d.pop("completion_webhook_id", UNSET)
        completion_webhook_id: UUID | Unset
        if isinstance(_completion_webhook_id, Unset):
            completion_webhook_id = UNSET
        else:
            completion_webhook_id = UUID(_completion_webhook_id)

        operation_definition_summary = cls(
            id=id,
            app_id=app_id,
            scope=scope,
            revision=revision,
            deployment_id=deployment_id,
            name=name,
            method=method,
            path=path,
            owner=owner,
            recovery=recovery,
            progress_stages=progress_stages,
            created_at=created_at,
            milestones=milestones,
            workflow_steps=workflow_steps,
            subject=subject,
            http_transaction_version=http_transaction_version,
            release_id=release_id,
            job=job,
            workflow=workflow,
            transaction_receipt=transaction_receipt,
            completion_webhook_id=completion_webhook_id,
        )

        operation_definition_summary.additional_properties = d
        return operation_definition_summary

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> Any:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: Any) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
