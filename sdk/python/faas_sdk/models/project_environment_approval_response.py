from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ProjectEnvironmentApprovalResponse")


@_attrs_define
class ProjectEnvironmentApprovalResponse:
    """Short-lived credential for applying the approved plan. The approval token is returned only here."""

    approval_token: str
    environment: str
    expires_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        approval_token = self.approval_token

        environment = self.environment

        expires_at = self.expires_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "approval_token": approval_token,
                "environment": environment,
                "expires_at": expires_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        approval_token = d.pop("approval_token")

        environment = d.pop("environment")

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        project_environment_approval_response = cls(
            approval_token=approval_token,
            environment=environment,
            expires_at=expires_at,
        )

        project_environment_approval_response.additional_properties = d
        return project_environment_approval_response

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
