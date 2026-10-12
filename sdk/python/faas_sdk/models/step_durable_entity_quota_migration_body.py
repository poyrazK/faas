from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="StepDurableEntityQuotaMigrationBody")


@_attrs_define
class StepDurableEntityQuotaMigrationBody:
    """Step durable entity quota migration — requestBody / content / application/json / schema: Preview mutation input with
    server-side size, scope and revision validation.

    """

    migration_id: UUID
    """Step durable entity quota migration — requestBody / content / application/json / schema / properties /
    migration id: Fresh operation identity; replay must retain this identity and the exact request."""
    expected_revision: int
    """Step durable entity quota migration — requestBody / content / application/json / schema / properties /
    expected revision: Exact observed numeric checkpoint revision; inspect after uncertain outcomes."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        migration_id = str(self.migration_id)

        expected_revision = self.expected_revision

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "migration_id": migration_id,
                "expected_revision": expected_revision,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        migration_id = UUID(d.pop("migration_id"))

        expected_revision = d.pop("expected_revision")

        step_durable_entity_quota_migration_body = cls(
            migration_id=migration_id,
            expected_revision=expected_revision,
        )

        step_durable_entity_quota_migration_body.additional_properties = d
        return step_durable_entity_quota_migration_body

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
