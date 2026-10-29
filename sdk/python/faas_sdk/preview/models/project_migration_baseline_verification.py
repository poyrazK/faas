from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.project_migration_baseline_verification_status import (
    ProjectMigrationBaselineVerificationStatus,
    check_project_migration_baseline_verification_status,
)

if TYPE_CHECKING:
    from ..models.project_release_probe_evidence import ProjectReleaseProbeEvidence


T = TypeVar("T", bound="ProjectMigrationBaselineVerification")


@_attrs_define
class ProjectMigrationBaselineVerification:
    """Durable post-expansion production probe checkpoint. Commands and database contents remain private."""

    task_id: str
    release_id: str
    started_at: datetime.datetime
    checked_at: datetime.datetime
    status: ProjectMigrationBaselineVerificationStatus
    evidence: list[ProjectReleaseProbeEvidence]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        task_id = self.task_id

        release_id = self.release_id

        started_at = self.started_at.isoformat()

        checked_at = self.checked_at.isoformat()

        status: str = self.status

        evidence = []
        for evidence_item_data in self.evidence:
            evidence_item = evidence_item_data.to_dict()
            evidence.append(evidence_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "task_id": task_id,
                "release_id": release_id,
                "started_at": started_at,
                "checked_at": checked_at,
                "status": status,
                "evidence": evidence,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.project_release_probe_evidence import ProjectReleaseProbeEvidence

        d = dict(src_dict)
        task_id = d.pop("task_id")

        release_id = d.pop("release_id")

        started_at = datetime.datetime.fromisoformat(d.pop("started_at"))

        checked_at = datetime.datetime.fromisoformat(d.pop("checked_at"))

        status = check_project_migration_baseline_verification_status(d.pop("status"))

        evidence = []
        _evidence = d.pop("evidence")
        for evidence_item_data in _evidence:
            evidence_item = ProjectReleaseProbeEvidence.from_dict(evidence_item_data)

            evidence.append(evidence_item)

        project_migration_baseline_verification = cls(
            task_id=task_id,
            release_id=release_id,
            started_at=started_at,
            checked_at=checked_at,
            status=status,
            evidence=evidence,
        )

        project_migration_baseline_verification.additional_properties = d
        return project_migration_baseline_verification

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
