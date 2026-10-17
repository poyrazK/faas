from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="UpgradeManagedPostgresDatabaseRequest")


@_attrs_define
class UpgradeManagedPostgresDatabaseRequest:
    """Canonical UUID request for an in-place upgrade of an existing managed PostgreSQL database to a later major."""

    request_id: UUID
    """Canonical nonzero UUID for this upgrade; replay with the same database and major after uncertain responses."""
    postgres_major: int
    """Target major; later than the database's and offered by its backend."""

    def to_dict(self) -> dict[str, Any]:
        request_id = str(self.request_id)

        postgres_major = self.postgres_major

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "request_id": request_id,
                "postgres_major": postgres_major,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        request_id = UUID(d.pop("request_id"))

        postgres_major = d.pop("postgres_major")

        upgrade_managed_postgres_database_request = cls(
            request_id=request_id,
            postgres_major=postgres_major,
        )

        return upgrade_managed_postgres_database_request
