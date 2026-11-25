from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="SetManagedPostgresBackupScheduleRequest")


@_attrs_define
class SetManagedPostgresBackupScheduleRequest:
    daily_at_utc: str
    bucket_app: str
    """App slug that owns the destination bucket."""
    bucket_id: UUID
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        daily_at_utc = self.daily_at_utc

        bucket_app = self.bucket_app

        bucket_id = str(self.bucket_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "daily_at_utc": daily_at_utc,
                "bucket_app": bucket_app,
                "bucket_id": bucket_id,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        daily_at_utc = d.pop("daily_at_utc")

        bucket_app = d.pop("bucket_app")

        bucket_id = UUID(d.pop("bucket_id"))

        set_managed_postgres_backup_schedule_request = cls(
            daily_at_utc=daily_at_utc,
            bucket_app=bucket_app,
            bucket_id=bucket_id,
        )

        set_managed_postgres_backup_schedule_request.additional_properties = d
        return set_managed_postgres_backup_schedule_request

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
