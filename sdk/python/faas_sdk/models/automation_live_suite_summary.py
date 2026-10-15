from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="AutomationLiveSuiteSummary")


@_attrs_define
class AutomationLiveSuiteSummary:
    """Retained suite identity and linked-run counts. Listing is not an assertion verdict."""

    id: UUID
    name: str
    version: int
    published_version: int
    deployment_id: UUID
    created_at: datetime.datetime
    updated_at: datetime.datetime
    case_count: int
    linked_runs: int
    managed: bool | Unset = UNSET
    """True for suites with durable server-managed admission. Metadata PUT cannot change these records;
    continuation requires explicit execution authorization."""

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        name = self.name

        version = self.version

        published_version = self.published_version

        deployment_id = str(self.deployment_id)

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        case_count = self.case_count

        linked_runs = self.linked_runs

        managed = self.managed

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "name": name,
                "version": version,
                "published_version": published_version,
                "deployment_id": deployment_id,
                "created_at": created_at,
                "updated_at": updated_at,
                "case_count": case_count,
                "linked_runs": linked_runs,
            }
        )
        if managed is not UNSET:
            field_dict["managed"] = managed

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        name = d.pop("name")

        version = d.pop("version")

        published_version = d.pop("published_version")

        deployment_id = UUID(d.pop("deployment_id"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        case_count = d.pop("case_count")

        linked_runs = d.pop("linked_runs")

        managed = d.pop("managed", UNSET)

        automation_live_suite_summary = cls(
            id=id,
            name=name,
            version=version,
            published_version=published_version,
            deployment_id=deployment_id,
            created_at=created_at,
            updated_at=updated_at,
            case_count=case_count,
            linked_runs=linked_runs,
            managed=managed,
        )

        return automation_live_suite_summary
