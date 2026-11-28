from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.migration_dns_record_type import MigrationDNSRecordType, check_migration_dns_record_type

T = TypeVar("T", bound="MigrationDNSRecord")


@_attrs_define
class MigrationDNSRecord:
    """Previous routing record at the production hostname. No URLs, credentials or TXT records."""

    type_: MigrationDNSRecordType
    value: str
    ttl: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        type_: str = self.type_

        value = self.value

        ttl = self.ttl

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "type": type_,
                "value": value,
                "ttl": ttl,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        type_ = check_migration_dns_record_type(d.pop("type"))

        value = d.pop("value")

        ttl = d.pop("ttl")

        migration_dns_record = cls(
            type_=type_,
            value=value,
            ttl=ttl,
        )

        migration_dns_record.additional_properties = d
        return migration_dns_record

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
