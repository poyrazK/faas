from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.managed_postgres_accounting_diagnostic import ManagedPostgresAccountingDiagnostic


T = TypeVar("T", bound="ManagedPostgresAccountingDiagnosticsResponse")


@_attrs_define
class ManagedPostgresAccountingDiagnosticsResponse:
    """One live account-scoped page of local accounting evidence."""

    account_id: UUID
    evaluated_at: datetime.datetime
    policy_enabled: bool
    window_seconds: int
    items: list[ManagedPostgresAccountingDiagnostic]
    next_cursor: None | Unset | UUID = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        account_id = str(self.account_id)

        evaluated_at = self.evaluated_at.isoformat()

        policy_enabled = self.policy_enabled

        window_seconds = self.window_seconds

        items = []
        for items_item_data in self.items:
            items_item = items_item_data.to_dict()
            items.append(items_item)

        next_cursor: None | str | Unset
        if isinstance(self.next_cursor, Unset):
            next_cursor = UNSET
        elif isinstance(self.next_cursor, UUID):
            next_cursor = str(self.next_cursor)
        else:
            next_cursor = self.next_cursor

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "account_id": account_id,
                "evaluated_at": evaluated_at,
                "policy_enabled": policy_enabled,
                "window_seconds": window_seconds,
                "items": items,
            }
        )
        if next_cursor is not UNSET:
            field_dict["next_cursor"] = next_cursor

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.managed_postgres_accounting_diagnostic import ManagedPostgresAccountingDiagnostic

        d = dict(src_dict)
        account_id = UUID(d.pop("account_id"))

        evaluated_at = datetime.datetime.fromisoformat(d.pop("evaluated_at"))

        policy_enabled = d.pop("policy_enabled")

        window_seconds = d.pop("window_seconds")

        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = ManagedPostgresAccountingDiagnostic.from_dict(items_item_data)

            items.append(items_item)

        def _parse_next_cursor(data: object) -> None | Unset | UUID:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                next_cursor_type_0 = UUID(data)

                return next_cursor_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(None | Unset | UUID, data)

        next_cursor = _parse_next_cursor(d.pop("next_cursor", UNSET))

        managed_postgres_accounting_diagnostics_response = cls(
            account_id=account_id,
            evaluated_at=evaluated_at,
            policy_enabled=policy_enabled,
            window_seconds=window_seconds,
            items=items,
            next_cursor=next_cursor,
        )

        managed_postgres_accounting_diagnostics_response.additional_properties = d
        return managed_postgres_accounting_diagnostics_response

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
