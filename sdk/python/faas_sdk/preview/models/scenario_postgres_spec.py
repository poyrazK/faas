from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.scenario_postgres_spec_version import ScenarioPostgresSpecVersion, check_scenario_postgres_spec_version

if TYPE_CHECKING:
    from ..models.scenario_postgres_table import ScenarioPostgresTable


T = TypeVar("T", bound="ScenarioPostgresSpec")


@_attrs_define
class ScenarioPostgresSpec:
    """Versioned fixture digest, selected table contract and sampled lock-wait assertion."""

    version: ScenarioPostgresSpecVersion
    fixture_sha256: str
    tables: list[ScenarioPostgresTable]
    max_lock_wait_ms: int

    def to_dict(self) -> dict[str, Any]:
        version: int = self.version

        fixture_sha256 = self.fixture_sha256

        tables = []
        for tables_item_data in self.tables:
            tables_item = tables_item_data.to_dict()
            tables.append(tables_item)

        max_lock_wait_ms = self.max_lock_wait_ms

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "version": version,
                "fixture_sha256": fixture_sha256,
                "tables": tables,
                "max_lock_wait_ms": max_lock_wait_ms,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.scenario_postgres_table import ScenarioPostgresTable

        d = dict(src_dict)
        version = check_scenario_postgres_spec_version(d.pop("version"))

        fixture_sha256 = d.pop("fixture_sha256")

        tables = []
        _tables = d.pop("tables")
        for tables_item_data in _tables:
            tables_item = ScenarioPostgresTable.from_dict(tables_item_data)

            tables.append(tables_item)

        max_lock_wait_ms = d.pop("max_lock_wait_ms")

        scenario_postgres_spec = cls(
            version=version,
            fixture_sha256=fixture_sha256,
            tables=tables,
            max_lock_wait_ms=max_lock_wait_ms,
        )

        return scenario_postgres_spec
