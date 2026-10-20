from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.platform_tenant_reconciliation_plan_change import PlatformTenantReconciliationPlanChange


T = TypeVar("T", bound="PlatformTenantReconciliationPlanResponse")


@_attrs_define
class PlatformTenantReconciliationPlanResponse:
    """A deterministic, read-only plan and digest. The digest confirms this desired bundle and current ownership-aware
    state at apply time.

    """

    tenant_id: UUID
    plan_hash: str
    """SHA-256 confirmation token for this desired bundle and current plan."""
    changes: list[PlatformTenantReconciliationPlanChange]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        tenant_id = str(self.tenant_id)

        plan_hash = self.plan_hash

        changes = []
        for changes_item_data in self.changes:
            changes_item = changes_item_data.to_dict()
            changes.append(changes_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "tenant_id": tenant_id,
                "plan_hash": plan_hash,
                "changes": changes,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.platform_tenant_reconciliation_plan_change import PlatformTenantReconciliationPlanChange

        d = dict(src_dict)
        tenant_id = UUID(d.pop("tenant_id"))

        plan_hash = d.pop("plan_hash")

        changes = []
        _changes = d.pop("changes")
        for changes_item_data in _changes:
            changes_item = PlatformTenantReconciliationPlanChange.from_dict(changes_item_data)

            changes.append(changes_item)

        platform_tenant_reconciliation_plan_response = cls(
            tenant_id=tenant_id,
            plan_hash=plan_hash,
            changes=changes,
        )

        platform_tenant_reconciliation_plan_response.additional_properties = d
        return platform_tenant_reconciliation_plan_response

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
