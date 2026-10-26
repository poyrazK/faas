from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="CreateIssueIngestTokenRequest")


@_attrs_define
class CreateIssueIngestTokenRequest:
    """Create a short-lived reporting credential for one existing deployment and environment."""

    deployment_id: UUID
    name: str
    expires_at: datetime.datetime
    environment: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        deployment_id = str(self.deployment_id)

        name = self.name

        expires_at = self.expires_at.isoformat()

        environment = self.environment

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "deployment_id": deployment_id,
                "name": name,
                "expires_at": expires_at,
            }
        )
        if environment is not UNSET:
            field_dict["environment"] = environment

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        deployment_id = UUID(d.pop("deployment_id"))

        name = d.pop("name")

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        environment = d.pop("environment", UNSET)

        create_issue_ingest_token_request = cls(
            deployment_id=deployment_id,
            name=name,
            expires_at=expires_at,
            environment=environment,
        )

        create_issue_ingest_token_request.additional_properties = d
        return create_issue_ingest_token_request

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
