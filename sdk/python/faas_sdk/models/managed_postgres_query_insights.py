from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.managed_postgres_query_insight import ManagedPostgresQueryInsight


T = TypeVar("T", bound="ManagedPostgresQueryInsights")


@_attrs_define
class ManagedPostgresQueryInsights:
    database_id: UUID
    enabled: bool
    since: datetime.datetime
    observed_seconds: int
    queries: list[ManagedPostgresQueryInsight]
    last_sample_at: datetime.datetime | Unset = UNSET
    observed_through: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        database_id = str(self.database_id)

        enabled = self.enabled

        since = self.since.isoformat()

        observed_seconds = self.observed_seconds

        queries = []
        for queries_item_data in self.queries:
            queries_item = queries_item_data.to_dict()
            queries.append(queries_item)

        last_sample_at: str | Unset = UNSET
        if not isinstance(self.last_sample_at, Unset):
            last_sample_at = self.last_sample_at.isoformat()

        observed_through: str | Unset = UNSET
        if not isinstance(self.observed_through, Unset):
            observed_through = self.observed_through.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "database_id": database_id,
                "enabled": enabled,
                "since": since,
                "observed_seconds": observed_seconds,
                "queries": queries,
            }
        )
        if last_sample_at is not UNSET:
            field_dict["last_sample_at"] = last_sample_at
        if observed_through is not UNSET:
            field_dict["observed_through"] = observed_through

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.managed_postgres_query_insight import ManagedPostgresQueryInsight

        d = dict(src_dict)
        database_id = UUID(d.pop("database_id"))

        enabled = d.pop("enabled")

        since = datetime.datetime.fromisoformat(d.pop("since"))

        observed_seconds = d.pop("observed_seconds")

        queries = []
        _queries = d.pop("queries")
        for queries_item_data in _queries:
            queries_item = ManagedPostgresQueryInsight.from_dict(queries_item_data)

            queries.append(queries_item)

        _last_sample_at = d.pop("last_sample_at", UNSET)
        last_sample_at: datetime.datetime | Unset
        if isinstance(_last_sample_at, Unset):
            last_sample_at = UNSET
        else:
            last_sample_at = datetime.datetime.fromisoformat(_last_sample_at)

        _observed_through = d.pop("observed_through", UNSET)
        observed_through: datetime.datetime | Unset
        if isinstance(_observed_through, Unset):
            observed_through = UNSET
        else:
            observed_through = datetime.datetime.fromisoformat(_observed_through)

        managed_postgres_query_insights = cls(
            database_id=database_id,
            enabled=enabled,
            since=since,
            observed_seconds=observed_seconds,
            queries=queries,
            last_sample_at=last_sample_at,
            observed_through=observed_through,
        )

        managed_postgres_query_insights.additional_properties = d
        return managed_postgres_query_insights

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
