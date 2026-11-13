from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="AppPostgresBinding")


@_attrs_define
class AppPostgresBinding:
    """One live managed PostgreSQL binding of an app, without credential material."""

    id: str
    """The binding ID that revoking the binding takes."""
    database: str
    """The bound database's name."""
    scope: str
    environment_key: str
    access: str
    state: str
    """A binding in the deleting state no longer blocks deleting the app."""

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        database = self.database

        scope = self.scope

        environment_key = self.environment_key

        access = self.access

        state = self.state

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "database": database,
                "scope": scope,
                "environment_key": environment_key,
                "access": access,
                "state": state,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = d.pop("id")

        database = d.pop("database")

        scope = d.pop("scope")

        environment_key = d.pop("environment_key")

        access = d.pop("access")

        state = d.pop("state")

        app_postgres_binding = cls(
            id=id,
            database=database,
            scope=scope,
            environment_key=environment_key,
            access=access,
            state=state,
        )

        return app_postgres_binding
