from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.managed_postgres_export_state import ManagedPostgresExportState, check_managed_postgres_export_state
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.managed_postgres_export_manifest import ManagedPostgresExportManifest


T = TypeVar("T", bound="ManagedPostgresExport")


@_attrs_define
class ManagedPostgresExport:
    id: UUID
    state: ManagedPostgresExportState
    expires_at: datetime.datetime
    manifest: ManagedPostgresExportManifest | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        state: str = self.state

        expires_at = self.expires_at.isoformat()

        manifest: dict[str, Any] | Unset = UNSET
        if not isinstance(self.manifest, Unset):
            manifest = self.manifest.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "state": state,
                "expires_at": expires_at,
            }
        )
        if manifest is not UNSET:
            field_dict["manifest"] = manifest

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.managed_postgres_export_manifest import ManagedPostgresExportManifest

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        state = check_managed_postgres_export_state(d.pop("state"))

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        _manifest = d.pop("manifest", UNSET)
        manifest: ManagedPostgresExportManifest | Unset
        if isinstance(_manifest, Unset):
            manifest = UNSET
        else:
            manifest = ManagedPostgresExportManifest.from_dict(_manifest)

        managed_postgres_export = cls(
            id=id,
            state=state,
            expires_at=expires_at,
            manifest=manifest,
        )

        managed_postgres_export.additional_properties = d
        return managed_postgres_export

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
