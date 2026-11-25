from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="ManagedPostgresImportSource")


@_attrs_define
class ManagedPostgresImportSource:
    host: str
    """Public PostgreSQL endpoint with a certificate valid for this name."""
    port: int
    database: str
    user: str
    password: str
    skip_extensions: list[str] | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        host = self.host

        port = self.port

        database = self.database

        user = self.user

        password = self.password

        skip_extensions: list[str] | Unset = UNSET
        if not isinstance(self.skip_extensions, Unset):
            skip_extensions = self.skip_extensions

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "host": host,
                "port": port,
                "database": database,
                "user": user,
                "password": password,
            }
        )
        if skip_extensions is not UNSET:
            field_dict["skip_extensions"] = skip_extensions

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        host = d.pop("host")

        port = d.pop("port")

        database = d.pop("database")

        user = d.pop("user")

        password = d.pop("password")

        skip_extensions = cast(list[str], d.pop("skip_extensions", UNSET))

        managed_postgres_import_source = cls(
            host=host,
            port=port,
            database=database,
            user=user,
            password=password,
            skip_extensions=skip_extensions,
        )

        return managed_postgres_import_source
