from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="OutboundAIAccessToken")


@_attrs_define
class OutboundAIAccessToken:
    """An issued access token. prefix is the token's first 12 characters, to recognise it."""

    id: UUID
    name: str
    app_id: UUID
    prefix: str
    integration_ids: list[UUID]
    expires_at: datetime.datetime
    created_at: datetime.datetime
    last_used_at: datetime.datetime | None
    revoked_at: datetime.datetime | None
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        name = self.name

        app_id = str(self.app_id)

        prefix = self.prefix

        integration_ids = []
        for integration_ids_item_data in self.integration_ids:
            integration_ids_item = str(integration_ids_item_data)
            integration_ids.append(integration_ids_item)

        expires_at = self.expires_at.isoformat()

        created_at = self.created_at.isoformat()

        last_used_at: None | str
        if isinstance(self.last_used_at, datetime.datetime):
            last_used_at = self.last_used_at.isoformat()
        else:
            last_used_at = self.last_used_at

        revoked_at: None | str
        if isinstance(self.revoked_at, datetime.datetime):
            revoked_at = self.revoked_at.isoformat()
        else:
            revoked_at = self.revoked_at

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "name": name,
                "app_id": app_id,
                "prefix": prefix,
                "integration_ids": integration_ids,
                "expires_at": expires_at,
                "created_at": created_at,
                "last_used_at": last_used_at,
                "revoked_at": revoked_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        name = d.pop("name")

        app_id = UUID(d.pop("app_id"))

        prefix = d.pop("prefix")

        integration_ids = []
        _integration_ids = d.pop("integration_ids")
        for integration_ids_item_data in _integration_ids:
            integration_ids_item = UUID(integration_ids_item_data)

            integration_ids.append(integration_ids_item)

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

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

        outbound_ai_access_token = cls(
            id=id,
            name=name,
            app_id=app_id,
            prefix=prefix,
            integration_ids=integration_ids,
            expires_at=expires_at,
            created_at=created_at,
            last_used_at=last_used_at,
            revoked_at=revoked_at,
        )

        outbound_ai_access_token.additional_properties = d
        return outbound_ai_access_token

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
