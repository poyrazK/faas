from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ManagedPostgresQueryInsight")


@_attrs_define
class ManagedPostgresQueryInsight:
    """Aggregate execution statistics for one normalized statement."""

    query_id: str
    """PostgreSQL query ID within the indicated boot."""
    boot_at: datetime.datetime
    calls: int
    total_execution_ms: float
    mean_execution_ms: float
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        query_id = self.query_id

        boot_at = self.boot_at.isoformat()

        calls = self.calls

        total_execution_ms = self.total_execution_ms

        mean_execution_ms = self.mean_execution_ms

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "query_id": query_id,
                "boot_at": boot_at,
                "calls": calls,
                "total_execution_ms": total_execution_ms,
                "mean_execution_ms": mean_execution_ms,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        query_id = d.pop("query_id")

        boot_at = datetime.datetime.fromisoformat(d.pop("boot_at"))

        calls = d.pop("calls")

        total_execution_ms = d.pop("total_execution_ms")

        mean_execution_ms = d.pop("mean_execution_ms")

        managed_postgres_query_insight = cls(
            query_id=query_id,
            boot_at=boot_at,
            calls=calls,
            total_execution_ms=total_execution_ms,
            mean_execution_ms=mean_execution_ms,
        )

        managed_postgres_query_insight.additional_properties = d
        return managed_postgres_query_insight

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
