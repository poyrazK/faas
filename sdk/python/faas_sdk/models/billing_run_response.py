from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.billing_run_export import BillingRunExport, check_billing_run_export
from ..models.billing_run_response_status import BillingRunResponseStatus, check_billing_run_response_status
from ..models.billing_run_response_trigger import BillingRunResponseTrigger, check_billing_run_response_trigger
from ..models.billing_run_sources_item import BillingRunSourcesItem, check_billing_run_sources_item
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.billing_run_counts import BillingRunCounts


T = TypeVar("T", bound="BillingRunResponse")


@_attrs_define
class BillingRunResponse:
    """A billing run and its progress. A run completes when no customer is pending; held and failed customers do not fail
    it.

    """

    id: UUID
    month: str
    period_start: datetime.datetime
    period_end: datetime.datetime
    sources: list[BillingRunSourcesItem]
    """Which customers a run bills. With platform_tenants, a consumer's tenant-attributed usage is billed on the
    tenant statement and its app item is skipped."""
    finalize: bool
    export: BillingRunExport
    """Where finalized statements go: none, or invoices in the account's Stripe, Paddle or Lago (ADR-1507; requires
    finalize and a connected key)."""
    hold_on_gaps: bool
    trigger: BillingRunResponseTrigger
    status: BillingRunResponseStatus
    counts: BillingRunCounts
    """How many of a run's customers reached each outcome."""
    created_at: datetime.datetime
    started_at: datetime.datetime | Unset = UNSET
    completed_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        month = self.month

        period_start = self.period_start.isoformat()

        period_end = self.period_end.isoformat()

        sources = []
        for componentsschemas_billing_run_sources_item_data in self.sources:
            componentsschemas_billing_run_sources_item: str = componentsschemas_billing_run_sources_item_data
            sources.append(componentsschemas_billing_run_sources_item)

        finalize = self.finalize

        export: str = self.export

        hold_on_gaps = self.hold_on_gaps

        trigger: str = self.trigger

        status: str = self.status

        counts = self.counts.to_dict()

        created_at = self.created_at.isoformat()

        started_at: str | Unset = UNSET
        if not isinstance(self.started_at, Unset):
            started_at = self.started_at.isoformat()

        completed_at: str | Unset = UNSET
        if not isinstance(self.completed_at, Unset):
            completed_at = self.completed_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "month": month,
                "period_start": period_start,
                "period_end": period_end,
                "sources": sources,
                "finalize": finalize,
                "export": export,
                "hold_on_gaps": hold_on_gaps,
                "trigger": trigger,
                "status": status,
                "counts": counts,
                "created_at": created_at,
            }
        )
        if started_at is not UNSET:
            field_dict["started_at"] = started_at
        if completed_at is not UNSET:
            field_dict["completed_at"] = completed_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.billing_run_counts import BillingRunCounts

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        month = d.pop("month")

        period_start = datetime.datetime.fromisoformat(d.pop("period_start"))

        period_end = datetime.datetime.fromisoformat(d.pop("period_end"))

        sources = []
        _sources = d.pop("sources")
        for componentsschemas_billing_run_sources_item_data in _sources:
            componentsschemas_billing_run_sources_item = check_billing_run_sources_item(
                componentsschemas_billing_run_sources_item_data
            )

            sources.append(componentsschemas_billing_run_sources_item)

        finalize = d.pop("finalize")

        export = check_billing_run_export(d.pop("export"))

        hold_on_gaps = d.pop("hold_on_gaps")

        trigger = check_billing_run_response_trigger(d.pop("trigger"))

        status = check_billing_run_response_status(d.pop("status"))

        counts = BillingRunCounts.from_dict(d.pop("counts"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        _started_at = d.pop("started_at", UNSET)
        started_at: datetime.datetime | Unset
        if isinstance(_started_at, Unset):
            started_at = UNSET
        else:
            started_at = datetime.datetime.fromisoformat(_started_at)

        _completed_at = d.pop("completed_at", UNSET)
        completed_at: datetime.datetime | Unset
        if isinstance(_completed_at, Unset):
            completed_at = UNSET
        else:
            completed_at = datetime.datetime.fromisoformat(_completed_at)

        billing_run_response = cls(
            id=id,
            month=month,
            period_start=period_start,
            period_end=period_end,
            sources=sources,
            finalize=finalize,
            export=export,
            hold_on_gaps=hold_on_gaps,
            trigger=trigger,
            status=status,
            counts=counts,
            created_at=created_at,
            started_at=started_at,
            completed_at=completed_at,
        )

        billing_run_response.additional_properties = d
        return billing_run_response

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
