from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="CreateDataConnectionRequest")


@_attrs_define
class CreateDataConnectionRequest:
    """Source registration containing a verified-TLS PostgreSQL URL for sealing."""

    name: str
    connection_url: str
    """PostgreSQL URL with sslmode=verify-full. Stored encrypted and never returned. Source host and addresses must
    be operator-qualified."""

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        connection_url = self.connection_url

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "name": name,
                "connection_url": connection_url,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        name = d.pop("name")

        connection_url = d.pop("connection_url")

        create_data_connection_request = cls(
            name=name,
            connection_url=connection_url,
        )

        return create_data_connection_request
