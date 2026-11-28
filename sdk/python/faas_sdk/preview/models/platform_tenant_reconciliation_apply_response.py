from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.platform_tenant_reconciliation_plan_change import PlatformTenantReconciliationPlanChange


T = TypeVar("T", bound="PlatformTenantReconciliationApplyResponse")


@_attrs_define
class PlatformTenantReconciliationApplyResponse:
    """The confirmed changes applied in one transaction. Consumers and surfaces are detached, not deleted. receipt_id can
    be used to recover the result later.

    """

    tenant_id: UUID
    receipt_id: UUID
    """Durable identifier for this successful apply."""
    plan_hash: str
    applied_at: datetime.datetime
    applied: bool
    """True when the confirmed plan completed, including an already-converged no-op."""
    changes: list[PlatformTenantReconciliationPlanChange]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        tenant_id = str(self.tenant_id)

        receipt_id = str(self.receipt_id)

        plan_hash = self.plan_hash

        applied_at = self.applied_at.isoformat()

        applied = self.applied

        changes = []
        for changes_item_data in self.changes:
            changes_item = changes_item_data.to_dict()
            changes.append(changes_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "tenant_id": tenant_id,
                "receipt_id": receipt_id,
                "plan_hash": plan_hash,
                "applied_at": applied_at,
                "applied": applied,
                "changes": changes,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.platform_tenant_reconciliation_plan_change import PlatformTenantReconciliationPlanChange

        d = dict(src_dict)
        tenant_id = UUID(d.pop("tenant_id"))

        receipt_id = UUID(d.pop("receipt_id"))

        plan_hash = d.pop("plan_hash")

        applied_at = datetime.datetime.fromisoformat(d.pop("applied_at"))

        applied = d.pop("applied")

        changes = []
        _changes = d.pop("changes")
        for changes_item_data in _changes:
            changes_item = PlatformTenantReconciliationPlanChange.from_dict(changes_item_data)

            changes.append(changes_item)

        platform_tenant_reconciliation_apply_response = cls(
            tenant_id=tenant_id,
            receipt_id=receipt_id,
            plan_hash=plan_hash,
            applied_at=applied_at,
            applied=applied,
            changes=changes,
        )

        platform_tenant_reconciliation_apply_response.additional_properties = d
        return platform_tenant_reconciliation_apply_response

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
