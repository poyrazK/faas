from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.managed_postgres_import_status_state import (
    ManagedPostgresImportStatusState,
    check_managed_postgres_import_status_state,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.managed_postgres_extension_version import ManagedPostgresExtensionVersion


T = TypeVar("T", bound="ManagedPostgresImportStatus")


@_attrs_define
class ManagedPostgresImportStatus:
    """Import progress or the results of a compatibility check. The source login is never returned."""

    state: ManagedPostgresImportStatusState
    operation_id: UUID | Unset = UNSET
    error_code: str | Unset = UNSET
    source_major: int | Unset = UNSET
    started_at: datetime.datetime | Unset = UNSET
    finished_at: datetime.datetime | Unset = UNSET
    missing_extensions: list[str] | Unset = UNSET
    required_volume_bytes: int | Unset = UNSET
    incompatible_extensions: list[ManagedPostgresExtensionVersion] | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        state: str = self.state

        operation_id: str | Unset = UNSET
        if not isinstance(self.operation_id, Unset):
            operation_id = str(self.operation_id)

        error_code = self.error_code

        source_major = self.source_major

        started_at: str | Unset = UNSET
        if not isinstance(self.started_at, Unset):
            started_at = self.started_at.isoformat()

        finished_at: str | Unset = UNSET
        if not isinstance(self.finished_at, Unset):
            finished_at = self.finished_at.isoformat()

        missing_extensions: list[str] | Unset = UNSET
        if not isinstance(self.missing_extensions, Unset):
            missing_extensions = self.missing_extensions

        required_volume_bytes = self.required_volume_bytes

        incompatible_extensions: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.incompatible_extensions, Unset):
            incompatible_extensions = []
            for incompatible_extensions_item_data in self.incompatible_extensions:
                incompatible_extensions_item = incompatible_extensions_item_data.to_dict()
                incompatible_extensions.append(incompatible_extensions_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "state": state,
            }
        )
        if operation_id is not UNSET:
            field_dict["operation_id"] = operation_id
        if error_code is not UNSET:
            field_dict["error_code"] = error_code
        if source_major is not UNSET:
            field_dict["source_major"] = source_major
        if started_at is not UNSET:
            field_dict["started_at"] = started_at
        if finished_at is not UNSET:
            field_dict["finished_at"] = finished_at
        if missing_extensions is not UNSET:
            field_dict["missing_extensions"] = missing_extensions
        if required_volume_bytes is not UNSET:
            field_dict["required_volume_bytes"] = required_volume_bytes
        if incompatible_extensions is not UNSET:
            field_dict["incompatible_extensions"] = incompatible_extensions

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.managed_postgres_extension_version import ManagedPostgresExtensionVersion

        d = dict(src_dict)
        state = check_managed_postgres_import_status_state(d.pop("state"))

        _operation_id = d.pop("operation_id", UNSET)
        operation_id: UUID | Unset
        if isinstance(_operation_id, Unset):
            operation_id = UNSET
        else:
            operation_id = UUID(_operation_id)

        error_code = d.pop("error_code", UNSET)

        source_major = d.pop("source_major", UNSET)

        _started_at = d.pop("started_at", UNSET)
        started_at: datetime.datetime | Unset
        if isinstance(_started_at, Unset):
            started_at = UNSET
        else:
            started_at = datetime.datetime.fromisoformat(_started_at)

        _finished_at = d.pop("finished_at", UNSET)
        finished_at: datetime.datetime | Unset
        if isinstance(_finished_at, Unset):
            finished_at = UNSET
        else:
            finished_at = datetime.datetime.fromisoformat(_finished_at)

        missing_extensions = cast(list[str], d.pop("missing_extensions", UNSET))

        required_volume_bytes = d.pop("required_volume_bytes", UNSET)

        _incompatible_extensions = d.pop("incompatible_extensions", UNSET)
        incompatible_extensions: list[ManagedPostgresExtensionVersion] | Unset = UNSET
        if _incompatible_extensions is not UNSET:
            incompatible_extensions = []
            for incompatible_extensions_item_data in _incompatible_extensions:
                incompatible_extensions_item = ManagedPostgresExtensionVersion.from_dict(
                    incompatible_extensions_item_data
                )

                incompatible_extensions.append(incompatible_extensions_item)

        managed_postgres_import_status = cls(
            state=state,
            operation_id=operation_id,
            error_code=error_code,
            source_major=source_major,
            started_at=started_at,
            finished_at=finished_at,
            missing_extensions=missing_extensions,
            required_volume_bytes=required_volume_bytes,
            incompatible_extensions=incompatible_extensions,
        )

        managed_postgres_import_status.additional_properties = d
        return managed_postgres_import_status

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
