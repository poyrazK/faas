from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.billing_catalog_entry_kind import BillingCatalogEntryKind, check_billing_catalog_entry_kind
from ..models.billing_catalog_entry_plan import BillingCatalogEntryPlan, check_billing_catalog_entry_plan

T = TypeVar("T", bound="BillingCatalogEntry")


@_attrs_define
class BillingCatalogEntry:
    """One row in the provider price + product catalog (PR-P3).
    Plan values are the billable api.Plan constants
    ("hobby", "pro", "scale") — PlanFree is intentionally
    absent because it carries no recurring line item.
    Handle is the provider-side product or price ID. SyncedAt is
    RFC 3339 UTC from the catalog's last successful preflight.

    """

    plan: BillingCatalogEntryPlan
    kind: BillingCatalogEntryKind
    handle: str
    """Provider price or product ID."""
    synced_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        plan: str = self.plan

        kind: str = self.kind

        handle = self.handle

        synced_at = self.synced_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "plan": plan,
                "kind": kind,
                "handle": handle,
                "synced_at": synced_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        plan = check_billing_catalog_entry_plan(d.pop("plan"))

        kind = check_billing_catalog_entry_kind(d.pop("kind"))

        handle = d.pop("handle")

        synced_at = datetime.datetime.fromisoformat(d.pop("synced_at"))

        billing_catalog_entry = cls(
            plan=plan,
            kind=kind,
            handle=handle,
            synced_at=synced_at,
        )

        billing_catalog_entry.additional_properties = d
        return billing_catalog_entry

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
