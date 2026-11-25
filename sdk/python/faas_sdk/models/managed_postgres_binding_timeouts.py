from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.managed_postgres_binding_timeouts_state import (
    ManagedPostgresBindingTimeoutsState,
    check_managed_postgres_binding_timeouts_state,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ManagedPostgresBindingTimeouts")


@_attrs_define
class ManagedPostgresBindingTimeouts:
    binding_id: str
    statement_timeout_ms: int
    lock_timeout_ms: int
    idle_in_transaction_session_timeout_ms: int
    revision: int
    state: ManagedPostgresBindingTimeoutsState
    """Default means no policy was saved. Pending means the defaults are saved and awaiting application. Refreshing
    means PostgreSQL accepted the settings and the pool's cache may still expire. Applied means the refresh window
    elapsed and new transactions use fresh backends."""
    applied_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        binding_id = self.binding_id

        statement_timeout_ms = self.statement_timeout_ms

        lock_timeout_ms = self.lock_timeout_ms

        idle_in_transaction_session_timeout_ms = self.idle_in_transaction_session_timeout_ms

        revision = self.revision

        state: str = self.state

        applied_at: str | Unset = UNSET
        if not isinstance(self.applied_at, Unset):
            applied_at = self.applied_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "binding_id": binding_id,
                "statement_timeout_ms": statement_timeout_ms,
                "lock_timeout_ms": lock_timeout_ms,
                "idle_in_transaction_session_timeout_ms": idle_in_transaction_session_timeout_ms,
                "revision": revision,
                "state": state,
            }
        )
        if applied_at is not UNSET:
            field_dict["applied_at"] = applied_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        binding_id = d.pop("binding_id")

        statement_timeout_ms = d.pop("statement_timeout_ms")

        lock_timeout_ms = d.pop("lock_timeout_ms")

        idle_in_transaction_session_timeout_ms = d.pop("idle_in_transaction_session_timeout_ms")

        revision = d.pop("revision")

        state = check_managed_postgres_binding_timeouts_state(d.pop("state"))

        _applied_at = d.pop("applied_at", UNSET)
        applied_at: datetime.datetime | Unset
        if isinstance(_applied_at, Unset):
            applied_at = UNSET
        else:
            applied_at = datetime.datetime.fromisoformat(_applied_at)

        managed_postgres_binding_timeouts = cls(
            binding_id=binding_id,
            statement_timeout_ms=statement_timeout_ms,
            lock_timeout_ms=lock_timeout_ms,
            idle_in_transaction_session_timeout_ms=idle_in_transaction_session_timeout_ms,
            revision=revision,
            state=state,
            applied_at=applied_at,
        )

        managed_postgres_binding_timeouts.additional_properties = d
        return managed_postgres_binding_timeouts

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
