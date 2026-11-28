from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.project_environment_approval_status_response_status import (
    ProjectEnvironmentApprovalStatusResponseStatus,
    check_project_environment_approval_status_response_status,
)
from ..models.project_environment_approval_status_response_token_kind import (
    ProjectEnvironmentApprovalStatusResponseTokenKind,
    check_project_environment_approval_status_response_token_kind,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ProjectEnvironmentApprovalStatusResponse")


@_attrs_define
class ProjectEnvironmentApprovalStatusResponse:
    """Durable, non-secret lifecycle status for one environment approval."""

    approval_id: UUID
    environment: str
    token_kind: ProjectEnvironmentApprovalStatusResponseTokenKind
    status: ProjectEnvironmentApprovalStatusResponseStatus
    created_at: datetime.datetime
    expires_at: datetime.datetime
    consumed_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        approval_id = str(self.approval_id)

        environment = self.environment

        token_kind: str = self.token_kind

        status: str = self.status

        created_at = self.created_at.isoformat()

        expires_at = self.expires_at.isoformat()

        consumed_at: str | Unset = UNSET
        if not isinstance(self.consumed_at, Unset):
            consumed_at = self.consumed_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "approval_id": approval_id,
                "environment": environment,
                "token_kind": token_kind,
                "status": status,
                "created_at": created_at,
                "expires_at": expires_at,
            }
        )
        if consumed_at is not UNSET:
            field_dict["consumed_at"] = consumed_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        approval_id = UUID(d.pop("approval_id"))

        environment = d.pop("environment")

        token_kind = check_project_environment_approval_status_response_token_kind(d.pop("token_kind"))

        status = check_project_environment_approval_status_response_status(d.pop("status"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        _consumed_at = d.pop("consumed_at", UNSET)
        consumed_at: datetime.datetime | Unset
        if isinstance(_consumed_at, Unset):
            consumed_at = UNSET
        else:
            consumed_at = datetime.datetime.fromisoformat(_consumed_at)

        project_environment_approval_status_response = cls(
            approval_id=approval_id,
            environment=environment,
            token_kind=token_kind,
            status=status,
            created_at=created_at,
            expires_at=expires_at,
            consumed_at=consumed_at,
        )

        project_environment_approval_status_response.additional_properties = d
        return project_environment_approval_status_response

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
