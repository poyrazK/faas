from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.api_key_export_response_scopes_item import (
    APIKeyExportResponseScopesItem,
    check_api_key_export_response_scopes_item,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="APIKeyExportResponse")


@_attrs_define
class APIKeyExportResponse:
    """One API key in export form: id, prefix (first 8 chars), label, scopes, created/last-used timestamps, and request
    count. Scopes is the permission set attached to the key at the moment of export (audit trail; IAM-1, ADR-034 rev2).

        Example:
            {'id': '0123456789abcdef0123456789abcdef', 'prefix': 'fp_live_ab12cd34', 'label': 'ci-deploy', 'scopes':
                ['apps:read', 'deploy:write'], 'created_at': '2026-07-25T13:25:00Z', 'last_used_at': '2026-07-25T13:25:00Z'}

    """

    id: str
    prefix: str
    scopes: list[APIKeyExportResponseScopesItem]
    """Permission set attached to the exported key. Object-storage data scopes additionally require an explicit
    per-bucket grant; admin remains full access."""
    created_at: datetime.datetime
    label: None | str | Unset = UNSET
    last_used_at: datetime.datetime | None | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        prefix = self.prefix

        scopes = []
        for scopes_item_data in self.scopes:
            scopes_item: str = scopes_item_data
            scopes.append(scopes_item)

        created_at = self.created_at.isoformat()

        label: None | str | Unset
        if isinstance(self.label, Unset):
            label = UNSET
        else:
            label = self.label

        last_used_at: None | str | Unset
        if isinstance(self.last_used_at, Unset):
            last_used_at = UNSET
        elif isinstance(self.last_used_at, datetime.datetime):
            last_used_at = self.last_used_at.isoformat()
        else:
            last_used_at = self.last_used_at

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "prefix": prefix,
                "scopes": scopes,
                "created_at": created_at,
            }
        )
        if label is not UNSET:
            field_dict["label"] = label
        if last_used_at is not UNSET:
            field_dict["last_used_at"] = last_used_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = d.pop("id")

        prefix = d.pop("prefix")

        scopes = []
        _scopes = d.pop("scopes")
        for scopes_item_data in _scopes:
            scopes_item = check_api_key_export_response_scopes_item(scopes_item_data)

            scopes.append(scopes_item)

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        def _parse_label(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        label = _parse_label(d.pop("label", UNSET))

        def _parse_last_used_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                last_used_at_type_0 = datetime.datetime.fromisoformat(data)

                return last_used_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        last_used_at = _parse_last_used_at(d.pop("last_used_at", UNSET))

        api_key_export_response = cls(
            id=id,
            prefix=prefix,
            scopes=scopes,
            created_at=created_at,
            label=label,
            last_used_at=last_used_at,
        )

        api_key_export_response.additional_properties = d
        return api_key_export_response

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
