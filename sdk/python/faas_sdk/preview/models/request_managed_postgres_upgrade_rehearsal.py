from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="RequestManagedPostgresUpgradeRehearsal")


@_attrs_define
class RequestManagedPostgresUpgradeRehearsal:
    """Target PostgreSQL major and request identifier for a private upgrade rehearsal."""

    request_id: UUID
    """Reuse when retrying a request with the same source and target major."""
    postgres_major: int
    """Target major version."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        request_id = str(self.request_id)

        postgres_major = self.postgres_major

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "request_id": request_id,
                "postgres_major": postgres_major,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        request_id = UUID(d.pop("request_id"))

        postgres_major = d.pop("postgres_major")

        request_managed_postgres_upgrade_rehearsal = cls(
            request_id=request_id,
            postgres_major=postgres_major,
        )

        request_managed_postgres_upgrade_rehearsal.additional_properties = d
        return request_managed_postgres_upgrade_rehearsal

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
