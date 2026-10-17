from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.managed_postgres_live_key import ManagedPostgresLiveKey


T = TypeVar("T", bound="ManagedPostgresLiveQueries")


@_attrs_define
class ManagedPostgresLiveQueries:
    """Live-query state of a managed database (ADR-1384). secret appears only in the response that issues it."""

    database_id: str
    enabled: bool
    keys: list[ManagedPostgresLiveKey]
    secret: str | Unset = UNSET
    """The new live secret (glsk1_...). Store it as a backend secret; it cannot be read again."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        database_id = self.database_id

        enabled = self.enabled

        keys = []
        for keys_item_data in self.keys:
            keys_item = keys_item_data.to_dict()
            keys.append(keys_item)

        secret = self.secret

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "database_id": database_id,
                "enabled": enabled,
                "keys": keys,
            }
        )
        if secret is not UNSET:
            field_dict["secret"] = secret

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.managed_postgres_live_key import ManagedPostgresLiveKey

        d = dict(src_dict)
        database_id = d.pop("database_id")

        enabled = d.pop("enabled")

        keys = []
        _keys = d.pop("keys")
        for keys_item_data in _keys:
            keys_item = ManagedPostgresLiveKey.from_dict(keys_item_data)

            keys.append(keys_item)

        secret = d.pop("secret", UNSET)

        managed_postgres_live_queries = cls(
            database_id=database_id,
            enabled=enabled,
            keys=keys,
            secret=secret,
        )

        managed_postgres_live_queries.additional_properties = d
        return managed_postgres_live_queries

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
