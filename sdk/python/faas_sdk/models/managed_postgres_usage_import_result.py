from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ManagedPostgresUsageImportResult")


@_attrs_define
class ManagedPostgresUsageImportResult:
    """Cost changes and resulting coverage from preview or an immutable applied import receipt."""

    import_id: UUID
    database_id: UUID
    revision: str
    applied: bool
    window_count: int
    previous_cost_millicents: int
    imported_cost_millicents: int
    cost_delta_millicents: int
    collected_from: datetime.datetime
    collected_until: datetime.datetime
    observed_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        import_id = str(self.import_id)

        database_id = str(self.database_id)

        revision = self.revision

        applied = self.applied

        window_count = self.window_count

        previous_cost_millicents = self.previous_cost_millicents

        imported_cost_millicents = self.imported_cost_millicents

        cost_delta_millicents = self.cost_delta_millicents

        collected_from = self.collected_from.isoformat()

        collected_until = self.collected_until.isoformat()

        observed_at = self.observed_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "import_id": import_id,
                "database_id": database_id,
                "revision": revision,
                "applied": applied,
                "window_count": window_count,
                "previous_cost_millicents": previous_cost_millicents,
                "imported_cost_millicents": imported_cost_millicents,
                "cost_delta_millicents": cost_delta_millicents,
                "collected_from": collected_from,
                "collected_until": collected_until,
                "observed_at": observed_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        import_id = UUID(d.pop("import_id"))

        database_id = UUID(d.pop("database_id"))

        revision = d.pop("revision")

        applied = d.pop("applied")

        window_count = d.pop("window_count")

        previous_cost_millicents = d.pop("previous_cost_millicents")

        imported_cost_millicents = d.pop("imported_cost_millicents")

        cost_delta_millicents = d.pop("cost_delta_millicents")

        collected_from = datetime.datetime.fromisoformat(d.pop("collected_from"))

        collected_until = datetime.datetime.fromisoformat(d.pop("collected_until"))

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        managed_postgres_usage_import_result = cls(
            import_id=import_id,
            database_id=database_id,
            revision=revision,
            applied=applied,
            window_count=window_count,
            previous_cost_millicents=previous_cost_millicents,
            imported_cost_millicents=imported_cost_millicents,
            cost_delta_millicents=cost_delta_millicents,
            collected_from=collected_from,
            collected_until=collected_until,
            observed_at=observed_at,
        )

        managed_postgres_usage_import_result.additional_properties = d
        return managed_postgres_usage_import_result

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
