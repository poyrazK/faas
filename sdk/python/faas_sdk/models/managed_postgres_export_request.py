from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="ManagedPostgresExportRequest")


@_attrs_define
class ManagedPostgresExportRequest:
    request_id: UUID

    def to_dict(self) -> dict[str, Any]:
        request_id = str(self.request_id)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "request_id": request_id,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        request_id = UUID(d.pop("request_id"))

        managed_postgres_export_request = cls(
            request_id=request_id,
        )

        return managed_postgres_export_request
