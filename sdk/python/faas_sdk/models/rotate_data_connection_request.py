from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="RotateDataConnectionRequest")


@_attrs_define
class RotateDataConnectionRequest:
    """Revision-checked replacement credential for the same source database identity."""

    expected_revision: int
    connection_url: str
    """Replacement verified-TLS URL with an explicit password for the existing source host, database and username.
    addresses must be operator-qualified."""

    def to_dict(self) -> dict[str, Any]:
        expected_revision = self.expected_revision

        connection_url = self.connection_url

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "expected_revision": expected_revision,
                "connection_url": connection_url,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        expected_revision = d.pop("expected_revision")

        connection_url = d.pop("connection_url")

        rotate_data_connection_request = cls(
            expected_revision=expected_revision,
            connection_url=connection_url,
        )

        return rotate_data_connection_request
