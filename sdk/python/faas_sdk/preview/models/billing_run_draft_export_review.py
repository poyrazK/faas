from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.billing_run_draft_export_row import BillingRunDraftExportRow


T = TypeVar("T", bound="BillingRunDraftExportReview")


@_attrs_define
class BillingRunDraftExportReview:
    """One page of a run's review and the current draft-only connection, when available."""

    items: list[BillingRunDraftExportRow]
    next_cursor: str | Unset = UNSET
    livemode: bool | Unset = UNSET
    connection_updated_at: datetime.datetime | Unset = UNSET
    connection_problem: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        items = []
        for items_item_data in self.items:
            items_item = items_item_data.to_dict()
            items.append(items_item)

        next_cursor = self.next_cursor

        livemode = self.livemode

        connection_updated_at: str | Unset = UNSET
        if not isinstance(self.connection_updated_at, Unset):
            connection_updated_at = self.connection_updated_at.isoformat()

        connection_problem = self.connection_problem

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "items": items,
            }
        )
        if next_cursor is not UNSET:
            field_dict["next_cursor"] = next_cursor
        if livemode is not UNSET:
            field_dict["livemode"] = livemode
        if connection_updated_at is not UNSET:
            field_dict["connection_updated_at"] = connection_updated_at
        if connection_problem is not UNSET:
            field_dict["connection_problem"] = connection_problem

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.billing_run_draft_export_row import BillingRunDraftExportRow

        d = dict(src_dict)
        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = BillingRunDraftExportRow.from_dict(items_item_data)

            items.append(items_item)

        next_cursor = d.pop("next_cursor", UNSET)

        livemode = d.pop("livemode", UNSET)

        _connection_updated_at = d.pop("connection_updated_at", UNSET)
        connection_updated_at: datetime.datetime | Unset
        if isinstance(_connection_updated_at, Unset):
            connection_updated_at = UNSET
        else:
            connection_updated_at = datetime.datetime.fromisoformat(_connection_updated_at)

        connection_problem = d.pop("connection_problem", UNSET)

        billing_run_draft_export_review = cls(
            items=items,
            next_cursor=next_cursor,
            livemode=livemode,
            connection_updated_at=connection_updated_at,
            connection_problem=connection_problem,
        )

        billing_run_draft_export_review.additional_properties = d
        return billing_run_draft_export_review

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
