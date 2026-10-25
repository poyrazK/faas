from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="BillingPaddleOveragePreflightResponse")


@_attrs_define
class BillingPaddleOveragePreflightResponse:
    """Wire shape for GET /v1/admin/billing-paddle-overage/preflight
    (B4 / Tier 1 follow-up to PR #802). Operator-side guard that
    probes information_schema.columns for the four migration-00041
    columns + counts the per-state rows in a single round-trip.

    table_exists is false when the paddle_overage_dedupe table is
    entirely absent (migrations 00034 + 00041 both unapplied).
    has_window_start / has_state / has_claimed_at / has_claimed_by
    each correspond to one of the four columns added by
    migration 00041; all four must be true for the meterd
    overage pusher to land. pending_rows / completed_rows are
    the per-state row totals (a single SQL filter pair).

    """

    table_exists: bool
    """true if the paddle_overage_dedupe table is present (00034+)."""
    has_window_start: bool
    """true if the migration-00041 window_start column exists."""
    has_state: bool
    """true if the migration-00041 state column exists."""
    has_claimed_at: bool
    """true if the migration-00041 claimed_at column exists."""
    has_claimed_by: bool
    """true if the migration-00041 claimed_by column exists."""
    pending_rows: int
    """count(*) where state = pending."""
    completed_rows: int
    """count(*) where state = completed."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        table_exists = self.table_exists

        has_window_start = self.has_window_start

        has_state = self.has_state

        has_claimed_at = self.has_claimed_at

        has_claimed_by = self.has_claimed_by

        pending_rows = self.pending_rows

        completed_rows = self.completed_rows

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "table_exists": table_exists,
                "has_window_start": has_window_start,
                "has_state": has_state,
                "has_claimed_at": has_claimed_at,
                "has_claimed_by": has_claimed_by,
                "pending_rows": pending_rows,
                "completed_rows": completed_rows,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        table_exists = d.pop("table_exists")

        has_window_start = d.pop("has_window_start")

        has_state = d.pop("has_state")

        has_claimed_at = d.pop("has_claimed_at")

        has_claimed_by = d.pop("has_claimed_by")

        pending_rows = d.pop("pending_rows")

        completed_rows = d.pop("completed_rows")

        billing_paddle_overage_preflight_response = cls(
            table_exists=table_exists,
            has_window_start=has_window_start,
            has_state=has_state,
            has_claimed_at=has_claimed_at,
            has_claimed_by=has_claimed_by,
            pending_rows=pending_rows,
            completed_rows=completed_rows,
        )

        billing_paddle_overage_preflight_response.additional_properties = d
        return billing_paddle_overage_preflight_response

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
