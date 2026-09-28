from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="PlatformTenantReconciliationReceiptSummary")


@_attrs_define
class PlatformTenantReconciliationReceiptSummary:
    """Compact entry in a tenant's immutable reconciliation history."""

    receipt_id: UUID
    plan_hash: str
    applied_at: datetime.datetime
    change_count: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        receipt_id = str(self.receipt_id)

        plan_hash = self.plan_hash

        applied_at = self.applied_at.isoformat()

        change_count = self.change_count

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "receipt_id": receipt_id,
                "plan_hash": plan_hash,
                "applied_at": applied_at,
                "change_count": change_count,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        receipt_id = UUID(d.pop("receipt_id"))

        plan_hash = d.pop("plan_hash")

        applied_at = datetime.datetime.fromisoformat(d.pop("applied_at"))

        change_count = d.pop("change_count")

        platform_tenant_reconciliation_receipt_summary = cls(
            receipt_id=receipt_id,
            plan_hash=plan_hash,
            applied_at=applied_at,
            change_count=change_count,
        )

        platform_tenant_reconciliation_receipt_summary.additional_properties = d
        return platform_tenant_reconciliation_receipt_summary

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
