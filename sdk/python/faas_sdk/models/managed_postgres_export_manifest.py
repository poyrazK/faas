from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.managed_postgres_extension_version import ManagedPostgresExtensionVersion


T = TypeVar("T", bound="ManagedPostgresExportManifest")


@_attrs_define
class ManagedPostgresExportManifest:
    """Signed snapshot metadata and integrity checks for a portable export."""

    postgres_major: int
    captured_at: datetime.datetime
    extensions: list[ManagedPostgresExtensionVersion]
    size_bytes: int
    sha256: str
    database_bytes: int | Unset = UNSET
    """Source database size used to estimate restore space."""
    vector_schema: str | Unset = UNSET
    """The schema containing an installed vector extension."""
    restore_receipt: str | Unset = UNSET
    """Gregale's authentication of this archive and manifest for managed restore."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        postgres_major = self.postgres_major

        captured_at = self.captured_at.isoformat()

        extensions = []
        for extensions_item_data in self.extensions:
            extensions_item = extensions_item_data.to_dict()
            extensions.append(extensions_item)

        size_bytes = self.size_bytes

        sha256 = self.sha256

        database_bytes = self.database_bytes

        vector_schema = self.vector_schema

        restore_receipt = self.restore_receipt

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "postgres_major": postgres_major,
                "captured_at": captured_at,
                "extensions": extensions,
                "size_bytes": size_bytes,
                "sha256": sha256,
            }
        )
        if database_bytes is not UNSET:
            field_dict["database_bytes"] = database_bytes
        if vector_schema is not UNSET:
            field_dict["vector_schema"] = vector_schema
        if restore_receipt is not UNSET:
            field_dict["restore_receipt"] = restore_receipt

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.managed_postgres_extension_version import ManagedPostgresExtensionVersion

        d = dict(src_dict)
        postgres_major = d.pop("postgres_major")

        captured_at = datetime.datetime.fromisoformat(d.pop("captured_at"))

        extensions = []
        _extensions = d.pop("extensions")
        for extensions_item_data in _extensions:
            extensions_item = ManagedPostgresExtensionVersion.from_dict(extensions_item_data)

            extensions.append(extensions_item)

        size_bytes = d.pop("size_bytes")

        sha256 = d.pop("sha256")

        database_bytes = d.pop("database_bytes", UNSET)

        vector_schema = d.pop("vector_schema", UNSET)

        restore_receipt = d.pop("restore_receipt", UNSET)

        managed_postgres_export_manifest = cls(
            postgres_major=postgres_major,
            captured_at=captured_at,
            extensions=extensions,
            size_bytes=size_bytes,
            sha256=sha256,
            database_bytes=database_bytes,
            vector_schema=vector_schema,
            restore_receipt=restore_receipt,
        )

        managed_postgres_export_manifest.additional_properties = d
        return managed_postgres_export_manifest

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
