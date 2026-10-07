from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define

from ..models.operation_definition_spec_method import (
    OperationDefinitionSpecMethod,
    check_operation_definition_spec_method,
)
from ..models.operation_definition_spec_owner import OperationDefinitionSpecOwner, check_operation_definition_spec_owner
from ..models.operation_definition_spec_recovery import (
    OperationDefinitionSpecRecovery,
    check_operation_definition_spec_recovery,
)
from ..models.operation_definition_spec_transaction_receipt import (
    OperationDefinitionSpecTransactionReceipt,
    check_operation_definition_spec_transaction_receipt,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationDefinitionSpec")


@_attrs_define
class OperationDefinitionSpec:
    """Resolved immutable contract for an HTTP handler or a named linear HTTP workflow from the same deployment. Workflow
    definitions require POST ingress, reconciliation recovery and stages matching the steps. Ownership comes from
    verified authentication. Production admission remains disabled pending qualification.

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
    job: str | Unset = UNSET
    """Optional account-owned active batch Job. POST ingress and reconciliation recovery only; one task per
    generation. Input, image, command, environment and execution policy are frozen at admission. Mutually exclusive
    with workflow and transaction_receipt."""
    workflow: str | Unset = UNSET
    """Optional named workflow captured from this immutable deployment; path becomes its submission route."""
    transaction_receipt: OperationDefinitionSpecTransactionReceipt | Unset = UNSET
    """Explicit HTTP/PostgreSQL receipt adapter; requires reconciliation recovery. Business writes and the saved
    result commit in the customer database. Approved recovery replays a committed result without calling business
    code. This does not certify external effects or platform completion."""
    completion_webhook_id: UUID | Unset = UNSET
    recovery: OperationDefinitionSpecRecovery | Unset = "reconcile_on_unknown"

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        method: str = self.method

        path = self.path

        owner: str = self.owner

        input_schema = self.input_schema

        output_schema = self.output_schema

        progress_stages = self.progress_stages

        job = self.job

        workflow = self.workflow

        transaction_receipt: str | Unset = UNSET
        if not isinstance(self.transaction_receipt, Unset):
            transaction_receipt = self.transaction_receipt

        completion_webhook_id: str | Unset = UNSET
        if not isinstance(self.completion_webhook_id, Unset):
            completion_webhook_id = str(self.completion_webhook_id)

        recovery: str | Unset = UNSET
        if not isinstance(self.recovery, Unset):
            recovery = self.recovery

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
        if job is not UNSET:
            field_dict["job"] = job
        if workflow is not UNSET:
            field_dict["workflow"] = workflow
        if transaction_receipt is not UNSET:
            field_dict["transaction_receipt"] = transaction_receipt
        if completion_webhook_id is not UNSET:
            field_dict["completion_webhook_id"] = completion_webhook_id
        if recovery is not UNSET:
            field_dict["recovery"] = recovery

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        name = d.pop("name")

        method = check_operation_definition_spec_method(d.pop("method"))

        path = d.pop("path")

        owner = check_operation_definition_spec_owner(d.pop("owner"))

        input_schema = d.pop("input_schema")

        output_schema = d.pop("output_schema")

        progress_stages = cast(list[str], d.pop("progress_stages"))

        job = d.pop("job", UNSET)

        workflow = d.pop("workflow", UNSET)

        _transaction_receipt = d.pop("transaction_receipt", UNSET)
        transaction_receipt: OperationDefinitionSpecTransactionReceipt | Unset
        if isinstance(_transaction_receipt, Unset):
            transaction_receipt = UNSET
        else:
            transaction_receipt = check_operation_definition_spec_transaction_receipt(_transaction_receipt)

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

        operation_definition_spec = cls(
            name=name,
            method=method,
            path=path,
            owner=owner,
            input_schema=input_schema,
            output_schema=output_schema,
            progress_stages=progress_stages,
            job=job,
            workflow=workflow,
            transaction_receipt=transaction_receipt,
            completion_webhook_id=completion_webhook_id,
            recovery=recovery,
        )

        return operation_definition_spec
