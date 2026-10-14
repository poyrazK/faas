from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.realtime_worker_credential_permissions_item import (
    RealtimeWorkerCredentialPermissionsItem,
    check_realtime_worker_credential_permissions_item,
)

T = TypeVar("T", bound="RealtimeWorkerCredential")


@_attrs_define
class RealtimeWorkerCredential:
    """Realtime Worker Credential."""

    id: str
    group_id: str
    name: str
    permissions: list[RealtimeWorkerCredentialPermissionsItem]
    generation: str
    issued_at: datetime.datetime
    expires_at: datetime.datetime
    revoked_at: datetime.datetime | None
    last_used_at: datetime.datetime | None
    last_operation: None | str
    use_count: str
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        group_id = self.group_id

        name = self.name

        permissions = []
        for permissions_item_data in self.permissions:
            permissions_item: str = permissions_item_data
            permissions.append(permissions_item)

        generation = self.generation

        issued_at = self.issued_at.isoformat()

        expires_at = self.expires_at.isoformat()

        revoked_at: None | str
        if isinstance(self.revoked_at, datetime.datetime):
            revoked_at = self.revoked_at.isoformat()
        else:
            revoked_at = self.revoked_at

        last_used_at: None | str
        if isinstance(self.last_used_at, datetime.datetime):
            last_used_at = self.last_used_at.isoformat()
        else:
            last_used_at = self.last_used_at

        last_operation: None | str
        last_operation = self.last_operation

        use_count = self.use_count

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "group_id": group_id,
                "name": name,
                "permissions": permissions,
                "generation": generation,
                "issued_at": issued_at,
                "expires_at": expires_at,
                "revoked_at": revoked_at,
                "last_used_at": last_used_at,
                "last_operation": last_operation,
                "use_count": use_count,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = d.pop("id")

        group_id = d.pop("group_id")

        name = d.pop("name")

        permissions = []
        _permissions = d.pop("permissions")
        for permissions_item_data in _permissions:
            permissions_item = check_realtime_worker_credential_permissions_item(permissions_item_data)

            permissions.append(permissions_item)

        generation = d.pop("generation")

        issued_at = datetime.datetime.fromisoformat(d.pop("issued_at"))

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        def _parse_revoked_at(data: object) -> datetime.datetime | None:
            if data is None:
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                revoked_at_type_0 = datetime.datetime.fromisoformat(data)

                return revoked_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None, data)

        revoked_at = _parse_revoked_at(d.pop("revoked_at"))

        def _parse_last_used_at(data: object) -> datetime.datetime | None:
            if data is None:
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                last_used_at_type_0 = datetime.datetime.fromisoformat(data)

                return last_used_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None, data)

        last_used_at = _parse_last_used_at(d.pop("last_used_at"))

        def _parse_last_operation(data: object) -> None | str:
            if data is None:
                return data
            return cast(None | str, data)

        last_operation = _parse_last_operation(d.pop("last_operation"))

        use_count = d.pop("use_count")

        realtime_worker_credential = cls(
            id=id,
            group_id=group_id,
            name=name,
            permissions=permissions,
            generation=generation,
            issued_at=issued_at,
            expires_at=expires_at,
            revoked_at=revoked_at,
            last_used_at=last_used_at,
            last_operation=last_operation,
            use_count=use_count,
        )

        realtime_worker_credential.additional_properties = d
        return realtime_worker_credential

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
