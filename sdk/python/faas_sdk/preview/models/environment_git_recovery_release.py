from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.environment_git_recovery_release_eligibility import (
    EnvironmentGitRecoveryReleaseEligibility,
    check_environment_git_recovery_release_eligibility,
)
from ..models.environment_git_recovery_release_protection_status import (
    EnvironmentGitRecoveryReleaseProtectionStatus,
    check_environment_git_recovery_release_protection_status,
)
from ..models.environment_git_recovery_release_retention_status import (
    EnvironmentGitRecoveryReleaseRetentionStatus,
    check_environment_git_recovery_release_retention_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.environment_git_promotion_artifact import EnvironmentGitPromotionArtifact


T = TypeVar("T", bound="EnvironmentGitRecoveryRelease")


@_attrs_define
class EnvironmentGitRecoveryRelease:
    """One retained serving release assessed against the selected current target manifest."""

    release_set_id: UUID
    """Immutable release UUID within the current Git binding history."""
    active: bool
    """Whether this release is the environment's current active selection."""
    created_at: datetime.datetime
    """Creation time used with release UUID for stable descending recovery pagination."""
    retention_status: EnvironmentGitRecoveryReleaseRetentionStatus
    """Current selection or compatibility window status, separate from rollback eligibility."""
    commit_sha: str
    """Original manifest commit verified by historical serving evidence, or empty when unproven."""
    eligibility: EnvironmentGitRecoveryReleaseEligibility
    """Whether the reused rollback planner reports no blockers at observation time."""
    blocking_reasons: list[str]
    """Value-redacted rollback planner blockers for this retained release."""
    artifacts: list[EnvironmentGitPromotionArtifact]
    """Public immutable image and signed build identities checked for this recovery selection."""
    protection_status: EnvironmentGitRecoveryReleaseProtectionStatus | Unset = UNSET
    """Current artifact policy selection, independent of rollback eligibility and routing retention."""
    artifacts_protected_until: datetime.datetime | Unset = UNSET
    """Age deadline of current protection; a newer release or explicit policy change may displace it earlier."""
    retained_bytes: int | Unset = UNSET
    """Per-release charged layer bytes when currently protected; shared keys are charged once within each release."""
    expires_at: datetime.datetime | Unset = UNSET
    """Routing compatibility deadline for this release; does not guarantee artifact retention."""
    served_at: datetime.datetime | Unset = UNSET
    """Completion time of the selected release's historical serving receipt."""
    plan_hash: str | Unset = UNSET
    """Individual rollback observation hash; obtain a fresh plan and revalidate before export."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        release_set_id = str(self.release_set_id)

        active = self.active

        created_at = self.created_at.isoformat()

        retention_status: str = self.retention_status

        commit_sha = self.commit_sha

        eligibility: str = self.eligibility

        blocking_reasons = self.blocking_reasons

        artifacts = []
        for artifacts_item_data in self.artifacts:
            artifacts_item = artifacts_item_data.to_dict()
            artifacts.append(artifacts_item)

        protection_status: str | Unset = UNSET
        if not isinstance(self.protection_status, Unset):
            protection_status = self.protection_status

        artifacts_protected_until: str | Unset = UNSET
        if not isinstance(self.artifacts_protected_until, Unset):
            artifacts_protected_until = self.artifacts_protected_until.isoformat()

        retained_bytes = self.retained_bytes

        expires_at: str | Unset = UNSET
        if not isinstance(self.expires_at, Unset):
            expires_at = self.expires_at.isoformat()

        served_at: str | Unset = UNSET
        if not isinstance(self.served_at, Unset):
            served_at = self.served_at.isoformat()

        plan_hash = self.plan_hash

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "release_set_id": release_set_id,
                "active": active,
                "created_at": created_at,
                "retention_status": retention_status,
                "commit_sha": commit_sha,
                "eligibility": eligibility,
                "blocking_reasons": blocking_reasons,
                "artifacts": artifacts,
            }
        )
        if protection_status is not UNSET:
            field_dict["protection_status"] = protection_status
        if artifacts_protected_until is not UNSET:
            field_dict["artifacts_protected_until"] = artifacts_protected_until
        if retained_bytes is not UNSET:
            field_dict["retained_bytes"] = retained_bytes
        if expires_at is not UNSET:
            field_dict["expires_at"] = expires_at
        if served_at is not UNSET:
            field_dict["served_at"] = served_at
        if plan_hash is not UNSET:
            field_dict["plan_hash"] = plan_hash

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.environment_git_promotion_artifact import EnvironmentGitPromotionArtifact

        d = dict(src_dict)
        release_set_id = UUID(d.pop("release_set_id"))

        active = d.pop("active")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        retention_status = check_environment_git_recovery_release_retention_status(d.pop("retention_status"))

        commit_sha = d.pop("commit_sha")

        eligibility = check_environment_git_recovery_release_eligibility(d.pop("eligibility"))

        blocking_reasons = cast(list[str], d.pop("blocking_reasons"))

        artifacts = []
        _artifacts = d.pop("artifacts")
        for artifacts_item_data in _artifacts:
            artifacts_item = EnvironmentGitPromotionArtifact.from_dict(artifacts_item_data)

            artifacts.append(artifacts_item)

        _protection_status = d.pop("protection_status", UNSET)
        protection_status: EnvironmentGitRecoveryReleaseProtectionStatus | Unset
        if isinstance(_protection_status, Unset):
            protection_status = UNSET
        else:
            protection_status = check_environment_git_recovery_release_protection_status(_protection_status)

        _artifacts_protected_until = d.pop("artifacts_protected_until", UNSET)
        artifacts_protected_until: datetime.datetime | Unset
        if isinstance(_artifacts_protected_until, Unset):
            artifacts_protected_until = UNSET
        else:
            artifacts_protected_until = datetime.datetime.fromisoformat(_artifacts_protected_until)

        retained_bytes = d.pop("retained_bytes", UNSET)

        _expires_at = d.pop("expires_at", UNSET)
        expires_at: datetime.datetime | Unset
        if isinstance(_expires_at, Unset):
            expires_at = UNSET
        else:
            expires_at = datetime.datetime.fromisoformat(_expires_at)

        _served_at = d.pop("served_at", UNSET)
        served_at: datetime.datetime | Unset
        if isinstance(_served_at, Unset):
            served_at = UNSET
        else:
            served_at = datetime.datetime.fromisoformat(_served_at)

        plan_hash = d.pop("plan_hash", UNSET)

        environment_git_recovery_release = cls(
            release_set_id=release_set_id,
            active=active,
            created_at=created_at,
            retention_status=retention_status,
            commit_sha=commit_sha,
            eligibility=eligibility,
            blocking_reasons=blocking_reasons,
            artifacts=artifacts,
            protection_status=protection_status,
            artifacts_protected_until=artifacts_protected_until,
            retained_bytes=retained_bytes,
            expires_at=expires_at,
            served_at=served_at,
            plan_hash=plan_hash,
        )

        environment_git_recovery_release.additional_properties = d
        return environment_git_recovery_release

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
