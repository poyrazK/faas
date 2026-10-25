from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define

from ..models.scenario_postgres_table_schema import ScenarioPostgresTableSchema, check_scenario_postgres_table_schema

T = TypeVar("T", bound="ScenarioPostgresTable")


@_attrs_define
class ScenarioPostgresTable:
    """Explicit stable columns from one ordinary persistent table in the fixture database."""

    schema: ScenarioPostgresTableSchema
    name: str
    columns: list[str]

    def to_dict(self) -> dict[str, Any]:
        schema: str = self.schema

        name = self.name

        columns = self.columns

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "schema": schema,
                "name": name,
                "columns": columns,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        schema = check_scenario_postgres_table_schema(d.pop("schema"))

        name = d.pop("name")

        columns = cast(list[str], d.pop("columns"))

        scenario_postgres_table = cls(
            schema=schema,
            name=name,
            columns=columns,
        )

        return scenario_postgres_table
