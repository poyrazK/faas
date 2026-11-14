from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ServiceAbortReceipt")


@_attrs_define
class ServiceAbortReceipt:
    """Immutable acceptance of an original reviewed service-abort intent; ACK/drain completion and the current handoff
    remain separate.

    """

    attempt_id: str
    request_hash: str
    app_id: UUID
    deployment_id: UUID
    predecessor_deployment_id: UUID
    request_id: UUID
    scope: str
    """Default scope or the original named environment slug."""
    accepted_at: datetime.datetime
    project_id: UUID | Unset = UNSET
    """Original project identity; present for named scopes."""
    environment_id: UUID | Unset = UNSET
    """Original environment identity; present for named scopes."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        attempt_id = self.attempt_id

        request_hash = self.request_hash

        app_id = str(self.app_id)

        deployment_id = str(self.deployment_id)

        predecessor_deployment_id = str(self.predecessor_deployment_id)

        request_id = str(self.request_id)

        scope = self.scope

        accepted_at = self.accepted_at.isoformat()

        project_id: str | Unset = UNSET
        if not isinstance(self.project_id, Unset):
            project_id = str(self.project_id)

        environment_id: str | Unset = UNSET
        if not isinstance(self.environment_id, Unset):
            environment_id = str(self.environment_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "attempt_id": attempt_id,
                "request_hash": request_hash,
                "app_id": app_id,
                "deployment_id": deployment_id,
                "predecessor_deployment_id": predecessor_deployment_id,
                "request_id": request_id,
                "scope": scope,
                "accepted_at": accepted_at,
            }
        )
        if project_id is not UNSET:
            field_dict["project_id"] = project_id
        if environment_id is not UNSET:
            field_dict["environment_id"] = environment_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        attempt_id = d.pop("attempt_id")

        request_hash = d.pop("request_hash")

        app_id = UUID(d.pop("app_id"))

        deployment_id = UUID(d.pop("deployment_id"))

        predecessor_deployment_id = UUID(d.pop("predecessor_deployment_id"))

        request_id = UUID(d.pop("request_id"))

        scope = d.pop("scope")

        accepted_at = datetime.datetime.fromisoformat(d.pop("accepted_at"))

        _project_id = d.pop("project_id", UNSET)
        project_id: UUID | Unset
        if isinstance(_project_id, Unset):
            project_id = UNSET
        else:
            project_id = UUID(_project_id)

        _environment_id = d.pop("environment_id", UNSET)
        environment_id: UUID | Unset
        if isinstance(_environment_id, Unset):
            environment_id = UNSET
        else:
            environment_id = UUID(_environment_id)

        service_abort_receipt = cls(
            attempt_id=attempt_id,
            request_hash=request_hash,
            app_id=app_id,
            deployment_id=deployment_id,
            predecessor_deployment_id=predecessor_deployment_id,
            request_id=request_id,
            scope=scope,
            accepted_at=accepted_at,
            project_id=project_id,
            environment_id=environment_id,
        )

        service_abort_receipt.additional_properties = d
        return service_abort_receipt

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
