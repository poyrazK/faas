from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.managed_postgres_database_availability import (
    ManagedPostgresDatabaseAvailability,
    check_managed_postgres_database_availability,
)
from ..models.managed_postgres_database_service_class import (
    ManagedPostgresDatabaseServiceClass,
    check_managed_postgres_database_service_class,
)
from ..models.managed_postgres_database_state import ManagedPostgresDatabaseState, check_managed_postgres_database_state
from ..types import UNSET, Unset

T = TypeVar("T", bound="ManagedPostgresDatabase")


@_attrs_define
class ManagedPostgresDatabase:
    """Managed PostgreSQL metadata. Provider IDs and credentials are never returned."""

    id: str
    name: str
    region: str
    postgres_major: int
    service_class: ManagedPostgresDatabaseServiceClass
    availability: ManagedPostgresDatabaseAvailability
    scale_to_zero: bool
    storage_limit_bytes: int
    restore_window_seconds: int
    state: ManagedPostgresDatabaseState
    created_at: datetime.datetime
    updated_at: datetime.datetime
    restore_source_database_id: None | str | Unset = UNSET
    restore_point_in_time: datetime.datetime | None | Unset = UNSET
    last_error_code: None | str | Unset = UNSET
    deleted_at: datetime.datetime | None | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        name = self.name

        region = self.region

        postgres_major = self.postgres_major

        service_class: str = self.service_class

        availability: str = self.availability

        scale_to_zero = self.scale_to_zero

        storage_limit_bytes = self.storage_limit_bytes

        restore_window_seconds = self.restore_window_seconds

        state: str = self.state

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        restore_source_database_id: None | str | Unset
        if isinstance(self.restore_source_database_id, Unset):
            restore_source_database_id = UNSET
        else:
            restore_source_database_id = self.restore_source_database_id

        restore_point_in_time: None | str | Unset
        if isinstance(self.restore_point_in_time, Unset):
            restore_point_in_time = UNSET
        elif isinstance(self.restore_point_in_time, datetime.datetime):
            restore_point_in_time = self.restore_point_in_time.isoformat()
        else:
            restore_point_in_time = self.restore_point_in_time

        last_error_code: None | str | Unset
        if isinstance(self.last_error_code, Unset):
            last_error_code = UNSET
        else:
            last_error_code = self.last_error_code

        deleted_at: None | str | Unset
        if isinstance(self.deleted_at, Unset):
            deleted_at = UNSET
        elif isinstance(self.deleted_at, datetime.datetime):
            deleted_at = self.deleted_at.isoformat()
        else:
            deleted_at = self.deleted_at

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "name": name,
                "region": region,
                "postgres_major": postgres_major,
                "service_class": service_class,
                "availability": availability,
                "scale_to_zero": scale_to_zero,
                "storage_limit_bytes": storage_limit_bytes,
                "restore_window_seconds": restore_window_seconds,
                "state": state,
                "created_at": created_at,
                "updated_at": updated_at,
            }
        )
        if restore_source_database_id is not UNSET:
            field_dict["restore_source_database_id"] = restore_source_database_id
        if restore_point_in_time is not UNSET:
            field_dict["restore_point_in_time"] = restore_point_in_time
        if last_error_code is not UNSET:
            field_dict["last_error_code"] = last_error_code
        if deleted_at is not UNSET:
            field_dict["deleted_at"] = deleted_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = d.pop("id")

        name = d.pop("name")

        region = d.pop("region")

        postgres_major = d.pop("postgres_major")

        service_class = check_managed_postgres_database_service_class(d.pop("service_class"))

        availability = check_managed_postgres_database_availability(d.pop("availability"))

        scale_to_zero = d.pop("scale_to_zero")

        storage_limit_bytes = d.pop("storage_limit_bytes")

        restore_window_seconds = d.pop("restore_window_seconds")

        state = check_managed_postgres_database_state(d.pop("state"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        def _parse_restore_source_database_id(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        restore_source_database_id = _parse_restore_source_database_id(d.pop("restore_source_database_id", UNSET))

        def _parse_restore_point_in_time(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                restore_point_in_time_type_0 = datetime.datetime.fromisoformat(data)

                return restore_point_in_time_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        restore_point_in_time = _parse_restore_point_in_time(d.pop("restore_point_in_time", UNSET))

        def _parse_last_error_code(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        last_error_code = _parse_last_error_code(d.pop("last_error_code", UNSET))

        def _parse_deleted_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                deleted_at_type_0 = datetime.datetime.fromisoformat(data)

                return deleted_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        deleted_at = _parse_deleted_at(d.pop("deleted_at", UNSET))

        managed_postgres_database = cls(
            id=id,
            name=name,
            region=region,
            postgres_major=postgres_major,
            service_class=service_class,
            availability=availability,
            scale_to_zero=scale_to_zero,
            storage_limit_bytes=storage_limit_bytes,
            restore_window_seconds=restore_window_seconds,
            state=state,
            created_at=created_at,
            updated_at=updated_at,
            restore_source_database_id=restore_source_database_id,
            restore_point_in_time=restore_point_in_time,
            last_error_code=last_error_code,
            deleted_at=deleted_at,
        )

        managed_postgres_database.additional_properties = d
        return managed_postgres_database

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> Any:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: Any) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
