from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="PrepareManagedPostgresCutoverRequest")


@_attrs_define
class PrepareManagedPostgresCutoverRequest:
    """Stage source bindings for one app and scope on a ready database restored from that source."""

    source_database_id: UUID
    target_database_id: UUID
    app_id: UUID
    scope: str

    def to_dict(self) -> dict[str, Any]:
        source_database_id = str(self.source_database_id)

        target_database_id = str(self.target_database_id)

        app_id = str(self.app_id)

        scope = self.scope

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "source_database_id": source_database_id,
                "target_database_id": target_database_id,
                "app_id": app_id,
                "scope": scope,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        source_database_id = UUID(d.pop("source_database_id"))

        target_database_id = UUID(d.pop("target_database_id"))

        app_id = UUID(d.pop("app_id"))

        scope = d.pop("scope")

        prepare_managed_postgres_cutover_request = cls(
            source_database_id=source_database_id,
            target_database_id=target_database_id,
            app_id=app_id,
            scope=scope,
        )

        return prepare_managed_postgres_cutover_request
