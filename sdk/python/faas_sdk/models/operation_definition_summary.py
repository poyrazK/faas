from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

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
from ..types import UNSET, Unset

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
    release_id: UUID | Unset = UNSET
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

        release_id: str | Unset = UNSET
        if not isinstance(self.release_id, Unset):
            release_id = str(self.release_id)

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
        if release_id is not UNSET:
            field_dict["release_id"] = release_id
        if completion_webhook_id is not UNSET:
            field_dict["completion_webhook_id"] = completion_webhook_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
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

        _release_id = d.pop("release_id", UNSET)
        release_id: UUID | Unset
        if isinstance(_release_id, Unset):
            release_id = UNSET
        else:
            release_id = UUID(_release_id)

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
            release_id=release_id,
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
