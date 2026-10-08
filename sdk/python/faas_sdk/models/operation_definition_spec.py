from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define

from ..models.operation_definition_spec_http_transaction_version import (
    OperationDefinitionSpecHttpTransactionVersion,
    check_operation_definition_spec_http_transaction_version,
)
from ..models.operation_definition_spec_method import (
    OperationDefinitionSpecMethod,
    check_operation_definition_spec_method,
)
from ..models.operation_definition_spec_owner import OperationDefinitionSpecOwner, check_operation_definition_spec_owner
from ..models.operation_definition_spec_recovery import (
    OperationDefinitionSpecRecovery,
    check_operation_definition_spec_recovery,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_definition_spec_milestones import OperationDefinitionSpecMilestones
    from ..models.operation_subject_spec import OperationSubjectSpec
    from ..models.operation_workflow_step import OperationWorkflowStep


T = TypeVar("T", bound="OperationDefinitionSpec")


@_attrs_define
class OperationDefinitionSpec:
    """Resolved immutable contract for one HTTP handler. Ownership comes from verified authentication, never input fields.
    Production admission stays disabled until the HTTP execution adapter is qualified.

    """

    name: str
    method: OperationDefinitionSpecMethod
    path: str
    owner: OperationDefinitionSpecOwner
    input_schema: Any
    """Bundled JSON Schema 2020-12; only local document references are supported."""
    output_schema: Any
    """Bundled schema for the business result selected by the execution target."""
    progress_stages: list[str]
    milestones: OperationDefinitionSpecMilestones | Unset = UNSET
    """Declared public milestone names mapped to bundled JSON Schemas. Requires http_transaction_version 1. At most
    16 names, with an aggregate 16384 schema bytes."""
    workflow_steps: list[OperationWorkflowStep] | Unset = UNSET
    """Materialized workflow step mappings included in this compact definition view."""
    subject: OperationSubjectSpec | Unset = UNSET
    """Optional public business reference extracted once from validated input on new admission. This metadata does
    not authorize access to the business entity."""
    completion_webhook_id: UUID | Unset = UNSET
    recovery: OperationDefinitionSpecRecovery | Unset = "reconcile_on_unknown"
    http_transaction_version: OperationDefinitionSpecHttpTransactionVersion | Unset = UNSET
    """Opt into the internal customer Operation PostgreSQL receipt protocol. Version 1 saves and replays the full
    JSON business result; it does not negotiate managed operation envelopes or named effects."""

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        method: str = self.method

        path = self.path

        owner: str = self.owner

        input_schema = self.input_schema

        output_schema = self.output_schema

        progress_stages = self.progress_stages

        milestones: dict[str, Any] | Unset = UNSET
        if not isinstance(self.milestones, Unset):
            milestones = self.milestones.to_dict()

        workflow_steps: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.workflow_steps, Unset):
            workflow_steps = []
            for workflow_steps_item_data in self.workflow_steps:
                workflow_steps_item = workflow_steps_item_data.to_dict()
                workflow_steps.append(workflow_steps_item)

        subject: dict[str, Any] | Unset = UNSET
        if not isinstance(self.subject, Unset):
            subject = self.subject.to_dict()

        completion_webhook_id: str | Unset = UNSET
        if not isinstance(self.completion_webhook_id, Unset):
            completion_webhook_id = str(self.completion_webhook_id)

        recovery: str | Unset = UNSET
        if not isinstance(self.recovery, Unset):
            recovery = self.recovery

        http_transaction_version: int | Unset = UNSET
        if not isinstance(self.http_transaction_version, Unset):
            http_transaction_version = self.http_transaction_version

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "name": name,
                "method": method,
                "path": path,
                "owner": owner,
                "input_schema": input_schema,
                "output_schema": output_schema,
                "progress_stages": progress_stages,
            }
        )
        if milestones is not UNSET:
            field_dict["milestones"] = milestones
        if workflow_steps is not UNSET:
            field_dict["workflow_steps"] = workflow_steps
        if subject is not UNSET:
            field_dict["subject"] = subject
        if completion_webhook_id is not UNSET:
            field_dict["completion_webhook_id"] = completion_webhook_id
        if recovery is not UNSET:
            field_dict["recovery"] = recovery
        if http_transaction_version is not UNSET:
            field_dict["http_transaction_version"] = http_transaction_version

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_definition_spec_milestones import OperationDefinitionSpecMilestones
        from ..models.operation_subject_spec import OperationSubjectSpec
        from ..models.operation_workflow_step import OperationWorkflowStep

        d = dict(src_dict)
        name = d.pop("name")

        method = check_operation_definition_spec_method(d.pop("method"))

        path = d.pop("path")

        owner = check_operation_definition_spec_owner(d.pop("owner"))

        input_schema = d.pop("input_schema")

        output_schema = d.pop("output_schema")

        progress_stages = cast(list[str], d.pop("progress_stages"))

        _milestones = d.pop("milestones", UNSET)
        milestones: OperationDefinitionSpecMilestones | Unset
        if isinstance(_milestones, Unset):
            milestones = UNSET
        else:
            milestones = OperationDefinitionSpecMilestones.from_dict(_milestones)

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

        _completion_webhook_id = d.pop("completion_webhook_id", UNSET)
        completion_webhook_id: UUID | Unset
        if isinstance(_completion_webhook_id, Unset):
            completion_webhook_id = UNSET
        else:
            completion_webhook_id = UUID(_completion_webhook_id)

        _recovery = d.pop("recovery", UNSET)
        recovery: OperationDefinitionSpecRecovery | Unset
        if isinstance(_recovery, Unset):
            recovery = UNSET
        else:
            recovery = check_operation_definition_spec_recovery(_recovery)

        _http_transaction_version = d.pop("http_transaction_version", UNSET)
        http_transaction_version: OperationDefinitionSpecHttpTransactionVersion | Unset
        if isinstance(_http_transaction_version, Unset):
            http_transaction_version = UNSET
        else:
            http_transaction_version = check_operation_definition_spec_http_transaction_version(
                _http_transaction_version
            )

        operation_definition_spec = cls(
            name=name,
            method=method,
            path=path,
            owner=owner,
            input_schema=input_schema,
            output_schema=output_schema,
            progress_stages=progress_stages,
            milestones=milestones,
            workflow_steps=workflow_steps,
            subject=subject,
            completion_webhook_id=completion_webhook_id,
            recovery=recovery,
            http_transaction_version=http_transaction_version,
        )

        return operation_definition_spec
