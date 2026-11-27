from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.managed_postgres_lock_wait import ManagedPostgresLockWait


T = TypeVar("T", bound="ManagedPostgresActivitySample")


@_attrs_define
class ManagedPostgresActivitySample:
    """Connection usage covers the primary instance. Transactions and waiting sessions cover the managed database."""

    sampled_at: datetime.datetime
    connections: int
    """Client sessions across the primary."""
    connection_limit: int
    """Engine connection slots available to ordinary logins."""
    active_transactions: int
    oldest_active_transaction_ms: int
    idle_in_transaction: int
    oldest_idle_transaction_ms: int
    lock_waits: int
    blocked_sessions: list[ManagedPostgresLockWait]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        sampled_at = self.sampled_at.isoformat()

        connections = self.connections

        connection_limit = self.connection_limit

        active_transactions = self.active_transactions

        oldest_active_transaction_ms = self.oldest_active_transaction_ms

        idle_in_transaction = self.idle_in_transaction

        oldest_idle_transaction_ms = self.oldest_idle_transaction_ms

        lock_waits = self.lock_waits

        blocked_sessions = []
        for blocked_sessions_item_data in self.blocked_sessions:
            blocked_sessions_item = blocked_sessions_item_data.to_dict()
            blocked_sessions.append(blocked_sessions_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "sampled_at": sampled_at,
                "connections": connections,
                "connection_limit": connection_limit,
                "active_transactions": active_transactions,
                "oldest_active_transaction_ms": oldest_active_transaction_ms,
                "idle_in_transaction": idle_in_transaction,
                "oldest_idle_transaction_ms": oldest_idle_transaction_ms,
                "lock_waits": lock_waits,
                "blocked_sessions": blocked_sessions,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.managed_postgres_lock_wait import ManagedPostgresLockWait

        d = dict(src_dict)
        sampled_at = datetime.datetime.fromisoformat(d.pop("sampled_at"))

        connections = d.pop("connections")

        connection_limit = d.pop("connection_limit")

        active_transactions = d.pop("active_transactions")

        oldest_active_transaction_ms = d.pop("oldest_active_transaction_ms")

        idle_in_transaction = d.pop("idle_in_transaction")

        oldest_idle_transaction_ms = d.pop("oldest_idle_transaction_ms")

        lock_waits = d.pop("lock_waits")

        blocked_sessions = []
        _blocked_sessions = d.pop("blocked_sessions")
        for blocked_sessions_item_data in _blocked_sessions:
            blocked_sessions_item = ManagedPostgresLockWait.from_dict(blocked_sessions_item_data)

            blocked_sessions.append(blocked_sessions_item)

        managed_postgres_activity_sample = cls(
            sampled_at=sampled_at,
            connections=connections,
            connection_limit=connection_limit,
            active_transactions=active_transactions,
            oldest_active_transaction_ms=oldest_active_transaction_ms,
            idle_in_transaction=idle_in_transaction,
            oldest_idle_transaction_ms=oldest_idle_transaction_ms,
            lock_waits=lock_waits,
            blocked_sessions=blocked_sessions,
        )

        managed_postgres_activity_sample.additional_properties = d
        return managed_postgres_activity_sample

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
