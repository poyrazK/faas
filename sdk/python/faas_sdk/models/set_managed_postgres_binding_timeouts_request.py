from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="SetManagedPostgresBindingTimeoutsRequest")


@_attrs_define
class SetManagedPostgresBindingTimeoutsRequest:
    statement_timeout_ms: int | Unset = UNSET
    lock_timeout_ms: int | Unset = UNSET
    idle_in_transaction_session_timeout_ms: int | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        statement_timeout_ms = self.statement_timeout_ms

        lock_timeout_ms = self.lock_timeout_ms

        idle_in_transaction_session_timeout_ms = self.idle_in_transaction_session_timeout_ms

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if statement_timeout_ms is not UNSET:
            field_dict["statement_timeout_ms"] = statement_timeout_ms
        if lock_timeout_ms is not UNSET:
            field_dict["lock_timeout_ms"] = lock_timeout_ms
        if idle_in_transaction_session_timeout_ms is not UNSET:
            field_dict["idle_in_transaction_session_timeout_ms"] = idle_in_transaction_session_timeout_ms

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        statement_timeout_ms = d.pop("statement_timeout_ms", UNSET)

        lock_timeout_ms = d.pop("lock_timeout_ms", UNSET)

        idle_in_transaction_session_timeout_ms = d.pop("idle_in_transaction_session_timeout_ms", UNSET)

        set_managed_postgres_binding_timeouts_request = cls(
            statement_timeout_ms=statement_timeout_ms,
            lock_timeout_ms=lock_timeout_ms,
            idle_in_transaction_session_timeout_ms=idle_in_transaction_session_timeout_ms,
        )

        set_managed_postgres_binding_timeouts_request.additional_properties = d
        return set_managed_postgres_binding_timeouts_request

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
