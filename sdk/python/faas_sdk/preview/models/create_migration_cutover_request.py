from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.migration_dns_record import MigrationDNSRecord


T = TypeVar("T", bound="CreateMigrationCutoverRequest")


@_attrs_define
class CreateMigrationCutoverRequest:
    """Immutable production cutover plan. The stable ID reconciles lost responses. Preparation changes no traffic or domain
    bindings.

    """

    id: UUID
    verification_id: UUID
    hostname: str
    previous_records: list[MigrationDNSRecord]
    previous_proxy_enabled: bool
    max_5xx_rate_bps: int
    max_p95_ms: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        verification_id = str(self.verification_id)

        hostname = self.hostname

        previous_records = []
        for previous_records_item_data in self.previous_records:
            previous_records_item = previous_records_item_data.to_dict()
            previous_records.append(previous_records_item)

        previous_proxy_enabled = self.previous_proxy_enabled

        max_5xx_rate_bps = self.max_5xx_rate_bps

        max_p95_ms = self.max_p95_ms

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "verification_id": verification_id,
                "hostname": hostname,
                "previous_records": previous_records,
                "previous_proxy_enabled": previous_proxy_enabled,
                "max_5xx_rate_bps": max_5xx_rate_bps,
                "max_p95_ms": max_p95_ms,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.migration_dns_record import MigrationDNSRecord

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        verification_id = UUID(d.pop("verification_id"))

        hostname = d.pop("hostname")

        previous_records = []
        _previous_records = d.pop("previous_records")
        for previous_records_item_data in _previous_records:
            previous_records_item = MigrationDNSRecord.from_dict(previous_records_item_data)

            previous_records.append(previous_records_item)

        previous_proxy_enabled = d.pop("previous_proxy_enabled")

        max_5xx_rate_bps = d.pop("max_5xx_rate_bps")

        max_p95_ms = d.pop("max_p95_ms")

        create_migration_cutover_request = cls(
            id=id,
            verification_id=verification_id,
            hostname=hostname,
            previous_records=previous_records,
            previous_proxy_enabled=previous_proxy_enabled,
            max_5xx_rate_bps=max_5xx_rate_bps,
            max_p95_ms=max_p95_ms,
        )

        create_migration_cutover_request.additional_properties = d
        return create_migration_cutover_request

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
