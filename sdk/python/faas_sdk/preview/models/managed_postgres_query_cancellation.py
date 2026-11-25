from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.managed_postgres_query_cancellation_outcome import (
    ManagedPostgresQueryCancellationOutcome,
    check_managed_postgres_query_cancellation_outcome,
)

T = TypeVar("T", bound="ManagedPostgresQueryCancellation")


@_attrs_define
class ManagedPostgresQueryCancellation:
    """Recorded delivery outcome for a request to cancel a pooled query."""

    database_id: UUID
    target_id: UUID
    outcome: ManagedPostgresQueryCancellationOutcome
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        database_id = str(self.database_id)

        target_id = str(self.target_id)

        outcome: str = self.outcome

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "database_id": database_id,
                "target_id": target_id,
                "outcome": outcome,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        database_id = UUID(d.pop("database_id"))

        target_id = UUID(d.pop("target_id"))

        outcome = check_managed_postgres_query_cancellation_outcome(d.pop("outcome"))

        managed_postgres_query_cancellation = cls(
            database_id=database_id,
            target_id=target_id,
            outcome=outcome,
        )

        managed_postgres_query_cancellation.additional_properties = d
        return managed_postgres_query_cancellation

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
