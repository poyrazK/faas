from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="BillingRunCounts")


@_attrs_define
class BillingRunCounts:
    """How many of a run's customers reached each outcome."""

    pending: int
    drafted: int
    finalized: int
    exported: int
    up_to_date: int
    held: int
    skipped: int
    failed: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        pending = self.pending

        drafted = self.drafted

        finalized = self.finalized

        exported = self.exported

        up_to_date = self.up_to_date

        held = self.held

        skipped = self.skipped

        failed = self.failed

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "pending": pending,
                "drafted": drafted,
                "finalized": finalized,
                "exported": exported,
                "up_to_date": up_to_date,
                "held": held,
                "skipped": skipped,
                "failed": failed,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        pending = d.pop("pending")

        drafted = d.pop("drafted")

        finalized = d.pop("finalized")

        exported = d.pop("exported")

        up_to_date = d.pop("up_to_date")

        held = d.pop("held")

        skipped = d.pop("skipped")

        failed = d.pop("failed")

        billing_run_counts = cls(
            pending=pending,
            drafted=drafted,
            finalized=finalized,
            exported=exported,
            up_to_date=up_to_date,
            held=held,
            skipped=skipped,
            failed=failed,
        )

        billing_run_counts.additional_properties = d
        return billing_run_counts

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
