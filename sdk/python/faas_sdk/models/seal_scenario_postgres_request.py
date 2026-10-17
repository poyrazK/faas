from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="SealScenarioPostgresRequest")


@_attrs_define
class SealScenarioPostgresRequest:
    """Pinned deployment identity whose fixture effects are ready for independent collection."""

    deployment_id: UUID

    def to_dict(self) -> dict[str, Any]:
        deployment_id = str(self.deployment_id)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "deployment_id": deployment_id,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        deployment_id = UUID(d.pop("deployment_id"))

        seal_scenario_postgres_request = cls(
            deployment_id=deployment_id,
        )

        return seal_scenario_postgres_request
