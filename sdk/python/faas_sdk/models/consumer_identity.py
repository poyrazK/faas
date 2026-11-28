from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ConsumerIdentity")


@_attrs_define
class ConsumerIdentity:
    """An OAuth sign-in of an app's MCP server and the consumer it bills to."""

    id: UUID
    consumer_id: UUID
    issuer: str
    subject: str
    created_at: datetime.datetime
    updated_at: datetime.datetime
    email: str | Unset = UNSET
    """ The token's email claim; display only. """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        consumer_id = str(self.consumer_id)

        issuer = self.issuer

        subject = self.subject

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        email = self.email

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "consumer_id": consumer_id,
                "issuer": issuer,
                "subject": subject,
                "created_at": created_at,
                "updated_at": updated_at,
            }
        )
        if email is not UNSET:
            field_dict["email"] = email

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        consumer_id = UUID(d.pop("consumer_id"))

        issuer = d.pop("issuer")

        subject = d.pop("subject")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        email = d.pop("email", UNSET)

        consumer_identity = cls(
            id=id,
            consumer_id=consumer_id,
            issuer=issuer,
            subject=subject,
            created_at=created_at,
            updated_at=updated_at,
            email=email,
        )

        consumer_identity.additional_properties = d
        return consumer_identity

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
