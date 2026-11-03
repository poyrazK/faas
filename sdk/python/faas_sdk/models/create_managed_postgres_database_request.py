from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.create_managed_postgres_database_request_availability import (
    CreateManagedPostgresDatabaseRequestAvailability,
    check_create_managed_postgres_database_request_availability,
)
from ..models.create_managed_postgres_database_request_service_class import (
    CreateManagedPostgresDatabaseRequestServiceClass,
    check_create_managed_postgres_database_request_service_class,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="CreateManagedPostgresDatabaseRequest")


@_attrs_define
class CreateManagedPostgresDatabaseRequest:
    """Customer request to reserve a managed PostgreSQL database."""

    name: str
    region: str
    postgres_major: int | Unset = 16
    service_class: CreateManagedPostgresDatabaseRequestServiceClass | Unset = "development"
    availability: CreateManagedPostgresDatabaseRequestAvailability | Unset = "single_zone"
    scale_to_zero: bool | Unset = True
    storage_limit_bytes: int | Unset = UNSET
    restore_window_seconds: int | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        region = self.region

        postgres_major = self.postgres_major

        service_class: str | Unset = UNSET
        if not isinstance(self.service_class, Unset):
            service_class = self.service_class

        availability: str | Unset = UNSET
        if not isinstance(self.availability, Unset):
            availability = self.availability

        scale_to_zero = self.scale_to_zero

        storage_limit_bytes = self.storage_limit_bytes

        restore_window_seconds = self.restore_window_seconds

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "name": name,
                "region": region,
            }
        )
        if postgres_major is not UNSET:
            field_dict["postgres_major"] = postgres_major
        if service_class is not UNSET:
            field_dict["service_class"] = service_class
        if availability is not UNSET:
            field_dict["availability"] = availability
        if scale_to_zero is not UNSET:
            field_dict["scale_to_zero"] = scale_to_zero
        if storage_limit_bytes is not UNSET:
            field_dict["storage_limit_bytes"] = storage_limit_bytes
        if restore_window_seconds is not UNSET:
            field_dict["restore_window_seconds"] = restore_window_seconds

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        name = d.pop("name")

        region = d.pop("region")

        postgres_major = d.pop("postgres_major", UNSET)

        _service_class = d.pop("service_class", UNSET)
        service_class: CreateManagedPostgresDatabaseRequestServiceClass | Unset
        if isinstance(_service_class, Unset):
            service_class = UNSET
        else:
            service_class = check_create_managed_postgres_database_request_service_class(_service_class)

        _availability = d.pop("availability", UNSET)
        availability: CreateManagedPostgresDatabaseRequestAvailability | Unset
        if isinstance(_availability, Unset):
            availability = UNSET
        else:
            availability = check_create_managed_postgres_database_request_availability(_availability)

        scale_to_zero = d.pop("scale_to_zero", UNSET)

        storage_limit_bytes = d.pop("storage_limit_bytes", UNSET)

        restore_window_seconds = d.pop("restore_window_seconds", UNSET)

        create_managed_postgres_database_request = cls(
            name=name,
            region=region,
            postgres_major=postgres_major,
            service_class=service_class,
            availability=availability,
            scale_to_zero=scale_to_zero,
            storage_limit_bytes=storage_limit_bytes,
            restore_window_seconds=restore_window_seconds,
        )

        return create_managed_postgres_database_request
