from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.scenario_postgres_spec import ScenarioPostgresSpec


T = TypeVar("T", bound="CreateScenarioPostgresRequest")


@_attrs_define
class CreateScenarioPostgresRequest:
    """Sanitized fixture SQL and its exact collection contract for a fresh contained workload."""

    app_slug: str
    spec: ScenarioPostgresSpec
    """Versioned fixture digest, selected table contract and sampled lock-wait assertion."""
    sql: str
    """Sanitized UTF-8 fixture SQL; the server also enforces a 65536-byte limit."""

    def to_dict(self) -> dict[str, Any]:
        app_slug = self.app_slug

        spec = self.spec.to_dict()

        sql = self.sql

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "app_slug": app_slug,
                "spec": spec,
                "sql": sql,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.scenario_postgres_spec import ScenarioPostgresSpec

        d = dict(src_dict)
        app_slug = d.pop("app_slug")

        spec = ScenarioPostgresSpec.from_dict(d.pop("spec"))

        sql = d.pop("sql")

        create_scenario_postgres_request = cls(
            app_slug=app_slug,
            spec=spec,
            sql=sql,
        )

        return create_scenario_postgres_request
