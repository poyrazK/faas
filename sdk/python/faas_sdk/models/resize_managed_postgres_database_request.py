from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.resize_managed_postgres_database_request_service_class import (
    ResizeManagedPostgresDatabaseRequestServiceClass,
    check_resize_managed_postgres_database_request_service_class,
)

T = TypeVar("T", bound="ResizeManagedPostgresDatabaseRequest")


@_attrs_define
class ResizeManagedPostgresDatabaseRequest:
    """Canonical UUID request for a compute-class change on an existing managed PostgreSQL database."""

    request_id: UUID
    """Canonical nonzero UUID; reuse with the same database and target after uncertain responses."""
    service_class: ResizeManagedPostgresDatabaseRequestServiceClass

    def to_dict(self) -> dict[str, Any]:
        request_id = str(self.request_id)

        service_class: str = self.service_class

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "request_id": request_id,
                "service_class": service_class,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        request_id = UUID(d.pop("request_id"))

        service_class = check_resize_managed_postgres_database_request_service_class(d.pop("service_class"))

        resize_managed_postgres_database_request = cls(
            request_id=request_id,
            service_class=service_class,
        )

        return resize_managed_postgres_database_request
