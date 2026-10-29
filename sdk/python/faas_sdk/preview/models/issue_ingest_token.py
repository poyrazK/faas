from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="IssueIngestToken")


@_attrs_define
class IssueIngestToken:
    """Reporting credential metadata; the bearer secret appears only on initial creation."""

    id: UUID
    name: str
    app_id: UUID
    deployment_id: UUID
    environment: str
    expires_at: datetime.datetime
    revoked_at: datetime.datetime | Unset = UNSET
    token: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        name = self.name

        app_id = str(self.app_id)

        deployment_id = str(self.deployment_id)

        environment = self.environment

        expires_at = self.expires_at.isoformat()

        revoked_at: str | Unset = UNSET
        if not isinstance(self.revoked_at, Unset):
            revoked_at = self.revoked_at.isoformat()

        token = self.token

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "name": name,
                "app_id": app_id,
                "deployment_id": deployment_id,
                "environment": environment,
                "expires_at": expires_at,
            }
        )
        if revoked_at is not UNSET:
            field_dict["revoked_at"] = revoked_at
        if token is not UNSET:
            field_dict["token"] = token

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        name = d.pop("name")

        app_id = UUID(d.pop("app_id"))

        deployment_id = UUID(d.pop("deployment_id"))

        environment = d.pop("environment")

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        _revoked_at = d.pop("revoked_at", UNSET)
        revoked_at: datetime.datetime | Unset
        if isinstance(_revoked_at, Unset):
            revoked_at = UNSET
        else:
            revoked_at = datetime.datetime.fromisoformat(_revoked_at)

        token = d.pop("token", UNSET)

        issue_ingest_token = cls(
            id=id,
            name=name,
            app_id=app_id,
            deployment_id=deployment_id,
            environment=environment,
            expires_at=expires_at,
            revoked_at=revoked_at,
            token=token,
        )

        issue_ingest_token.additional_properties = d
        return issue_ingest_token

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
