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
from ..types import UNSET, Unset

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
            completion_webhook_id=completion_webhook_id,
            recovery=recovery,
        )

        return operation_definition_spec
