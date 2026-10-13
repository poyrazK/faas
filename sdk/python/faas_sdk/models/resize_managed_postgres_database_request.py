from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.resize_managed_postgres_database_request_service_class import (
    ResizeManagedPostgresDatabaseRequestServiceClass,
    check_resize_managed_postgres_database_request_service_class,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ResizeManagedPostgresDatabaseRequest")


@_attrs_define
class ResizeManagedPostgresDatabaseRequest:
    """Canonical UUID request for a compute-class change, a storage-limit increase, or both, on an existing managed
    PostgreSQL database. At least one of service_class and storage_limit_bytes is required.

    """

    request_id: UUID
    """Canonical nonzero UUID; reuse with the same database and target after uncertain responses."""
    service_class: ResizeManagedPostgresDatabaseRequestServiceClass | Unset = UNSET
    """Target class; omitted keeps the class."""
    storage_limit_bytes: int | Unset = UNSET
    """Target storage limit; omitted, zero or the current limit keeps it. Must not be lower than the current limit."""

    def to_dict(self) -> dict[str, Any]:
        request_id = str(self.request_id)

        service_class: str | Unset = UNSET
        if not isinstance(self.service_class, Unset):
            service_class = self.service_class

        storage_limit_bytes = self.storage_limit_bytes

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "request_id": request_id,
            }
        )
        if service_class is not UNSET:
            field_dict["service_class"] = service_class
        if storage_limit_bytes is not UNSET:
            field_dict["storage_limit_bytes"] = storage_limit_bytes

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        request_id = UUID(d.pop("request_id"))

        _service_class = d.pop("service_class", UNSET)
        service_class: ResizeManagedPostgresDatabaseRequestServiceClass | Unset
        if isinstance(_service_class, Unset):
            service_class = UNSET
        else:
            service_class = check_resize_managed_postgres_database_request_service_class(_service_class)

        storage_limit_bytes = d.pop("storage_limit_bytes", UNSET)

        resize_managed_postgres_database_request = cls(
            request_id=request_id,
            service_class=service_class,
            storage_limit_bytes=storage_limit_bytes,
        )

        return resize_managed_postgres_database_request
