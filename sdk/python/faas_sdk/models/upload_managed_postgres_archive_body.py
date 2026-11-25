from __future__ import annotations

import json
from collections.abc import Mapping
from io import BytesIO
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from .. import types
from ..types import File

if TYPE_CHECKING:
    from ..models.managed_postgres_export_manifest import ManagedPostgresExportManifest


T = TypeVar("T", bound="UploadManagedPostgresArchiveBody")


@_attrs_define
class UploadManagedPostgresArchiveBody:
    manifest: ManagedPostgresExportManifest
    archive: File
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        manifest = self.manifest.to_dict()

        archive = self.archive.to_tuple()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "manifest": manifest,
                "archive": archive,
            }
        )

        return field_dict

    def to_multipart(self) -> types.RequestFiles:
        files: types.RequestFiles = []

        files.append(("manifest", (None, json.dumps(self.manifest.to_dict()).encode(), "application/json")))

        files.append(("archive", self.archive.to_tuple()))

        for prop_name, prop in self.additional_properties.items():
            files.append((prop_name, (None, str(prop).encode(), "text/plain")))

        return files

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.managed_postgres_export_manifest import ManagedPostgresExportManifest

        d = dict(src_dict)
        manifest = ManagedPostgresExportManifest.from_dict(d.pop("manifest"))

        archive = File(payload=BytesIO(d.pop("archive")))

        upload_managed_postgres_archive_body = cls(
            manifest=manifest,
            archive=archive,
        )

        upload_managed_postgres_archive_body.additional_properties = d
        return upload_managed_postgres_archive_body

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
