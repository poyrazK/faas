from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.apply_durable_entity_quota_policy_body_limits import ApplyDurableEntityQuotaPolicyBodyLimits


T = TypeVar("T", bound="ApplyDurableEntityQuotaPolicyBody")


@_attrs_define
class ApplyDurableEntityQuotaPolicyBody:
    """Apply durable entity quota policy — requestBody / content / application/json / schema: Preview mutation input with
    server-side size, scope and revision validation.

    """

    operation_id: UUID
    """Apply durable entity quota policy — requestBody / content / application/json / schema / properties /
    operation id: Fresh operation identity; replay must retain this identity and the exact request."""
    expected_revision: int
    """Apply durable entity quota policy — requestBody / content / application/json / schema / properties /
    expected revision: Exact observed numeric checkpoint revision; inspect after uncertain outcomes."""
    expected_migration_revision: int
    """Apply durable entity quota policy — requestBody / content / application/json / schema / properties /
    expected migration revision: Exact observed numeric checkpoint revision; inspect after uncertain outcomes."""
    migration_id: UUID
    """Apply durable entity quota policy — requestBody / content / application/json / schema / properties /
    migration id: Fresh operation identity; replay must retain this identity and the exact request."""
    limits: ApplyDurableEntityQuotaPolicyBodyLimits
    """Apply durable entity quota policy — requestBody / content / application/json / schema / properties / limits:
    Proposed tenant admission caps; all fields are required."""
    expected_migration_id: str | Unset = UNSET
    """Apply durable entity quota policy — requestBody / content / application/json / schema / properties /
    expected migration id: Observed completed migration identity."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        operation_id = str(self.operation_id)

        expected_revision = self.expected_revision

        expected_migration_revision = self.expected_migration_revision

        migration_id = str(self.migration_id)

        limits = self.limits.to_dict()

        expected_migration_id = self.expected_migration_id

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "operation_id": operation_id,
                "expected_revision": expected_revision,
                "expected_migration_revision": expected_migration_revision,
                "migration_id": migration_id,
                "limits": limits,
            }
        )
        if expected_migration_id is not UNSET:
            field_dict["expected_migration_id"] = expected_migration_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.apply_durable_entity_quota_policy_body_limits import ApplyDurableEntityQuotaPolicyBodyLimits

        d = dict(src_dict)
        operation_id = UUID(d.pop("operation_id"))

        expected_revision = d.pop("expected_revision")

        expected_migration_revision = d.pop("expected_migration_revision")

        migration_id = UUID(d.pop("migration_id"))

        limits = ApplyDurableEntityQuotaPolicyBodyLimits.from_dict(d.pop("limits"))

        expected_migration_id = d.pop("expected_migration_id", UNSET)

        apply_durable_entity_quota_policy_body = cls(
            operation_id=operation_id,
            expected_revision=expected_revision,
            expected_migration_revision=expected_migration_revision,
            migration_id=migration_id,
            limits=limits,
            expected_migration_id=expected_migration_id,
        )

        apply_durable_entity_quota_policy_body.additional_properties = d
        return apply_durable_entity_quota_policy_body

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
