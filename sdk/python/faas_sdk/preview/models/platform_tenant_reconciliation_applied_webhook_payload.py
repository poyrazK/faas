from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="PlatformTenantReconciliationAppliedWebhookPayload")


@_attrs_define
class PlatformTenantReconciliationAppliedWebhookPayload:
    """Successful apply notification with the platform owner's stable tenant reference. Use receipt_id to fetch full
    changes; the event omits the submitted desired bundle and hostname challenge tokens.

    """

    platform_tenant_id: UUID
    external_ref: str
    receipt_id: UUID
    plan_hash: str
    applied_at: datetime.datetime
    change_count: int

    def to_dict(self) -> dict[str, Any]:
        platform_tenant_id = str(self.platform_tenant_id)

        external_ref = self.external_ref

        receipt_id = str(self.receipt_id)

        plan_hash = self.plan_hash

        applied_at = self.applied_at.isoformat()

        change_count = self.change_count

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "platform_tenant_id": platform_tenant_id,
                "external_ref": external_ref,
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
        platform_tenant_id = UUID(d.pop("platform_tenant_id"))

        external_ref = d.pop("external_ref")

        receipt_id = UUID(d.pop("receipt_id"))

        plan_hash = d.pop("plan_hash")

        applied_at = datetime.datetime.fromisoformat(d.pop("applied_at"))

        change_count = d.pop("change_count")

        platform_tenant_reconciliation_applied_webhook_payload = cls(
            platform_tenant_id=platform_tenant_id,
            external_ref=external_ref,
            receipt_id=receipt_id,
            plan_hash=plan_hash,
            applied_at=applied_at,
            change_count=change_count,
        )

        return platform_tenant_reconciliation_applied_webhook_payload
