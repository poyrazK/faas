from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.environment_git_ops_plan import EnvironmentGitOpsPlan
    from ..models.environment_git_promotion_artifact import EnvironmentGitPromotionArtifact
    from ..models.environment_git_promotion_change import EnvironmentGitPromotionChange
    from ..models.environment_git_promotion_origin import EnvironmentGitPromotionOrigin
    from ..models.environment_git_promotion_target import EnvironmentGitPromotionTarget


T = TypeVar("T", bound="PreviewEnvironmentGitRollbackResponse")


@_attrs_define
class PreviewEnvironmentGitRollbackResponse:
    """Read-only, value-redacted rollback proposal. It neither activates a release nor proves target readiness."""

    project: str
    """Project containing the selected historical release."""
    environment: str
    """Environment whose workload code would be restored."""
    observed_at: datetime.datetime
    """Time at which rollback selection and current target intent were observed."""
    read_only: bool
    """True because rollback planning does not change pins, intent or deployment status."""
    values_redacted: bool
    """Rollback output excludes variable values and secret-reference destinations."""
    requires_target_qualification: bool
    """Historical serving evidence selects code; the new target still needs fresh qualification."""
    current_release_set_id: str
    """Current active release identity, or empty when no release is active."""
    origin: EnvironmentGitPromotionOrigin
    """Scoped identity of the approved source release; graph and release IDs are empty if unproven."""
    target: EnvironmentGitPromotionTarget
    """Target binding and manifest observation; no target values are returned."""
    artifacts: list[EnvironmentGitPromotionArtifact]
    """Retained deployment and exact code artifact identities selected for rollback."""
    changes: list[EnvironmentGitPromotionChange]
    """Code-only source contract proposals."""
    target_plan: EnvironmentGitOpsPlan
    """Observation-bound plan; blocking reasons prevent mutation and overrides prevent convergence."""
    blocking_reasons: list[str]
    """Historical evidence, artifact or target conflicts that prevent rollback export."""
    plan_hash: str
    """Rollback-specific SHA256 of historical serving evidence, selected code and current target intent; never
    deployment authority."""
    origin_served_at: datetime.datetime | Unset = UNSET
    """Historical serving completion time; missing proof blocks export."""

    def to_dict(self) -> dict[str, Any]:
        project = self.project

        environment = self.environment

        observed_at = self.observed_at.isoformat()

        read_only = self.read_only

        values_redacted = self.values_redacted

        requires_target_qualification = self.requires_target_qualification

        current_release_set_id = self.current_release_set_id

        origin = self.origin.to_dict()

        target = self.target.to_dict()

        artifacts = []
        for artifacts_item_data in self.artifacts:
            artifacts_item = artifacts_item_data.to_dict()
            artifacts.append(artifacts_item)

        changes = []
        for changes_item_data in self.changes:
            changes_item = changes_item_data.to_dict()
            changes.append(changes_item)

        target_plan = self.target_plan.to_dict()

        blocking_reasons = self.blocking_reasons

        plan_hash = self.plan_hash

        origin_served_at: str | Unset = UNSET
        if not isinstance(self.origin_served_at, Unset):
            origin_served_at = self.origin_served_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "project": project,
                "environment": environment,
                "observed_at": observed_at,
                "read_only": read_only,
                "values_redacted": values_redacted,
                "requires_target_qualification": requires_target_qualification,
                "current_release_set_id": current_release_set_id,
                "origin": origin,
                "target": target,
                "artifacts": artifacts,
                "changes": changes,
                "target_plan": target_plan,
                "blocking_reasons": blocking_reasons,
                "plan_hash": plan_hash,
            }
        )
        if origin_served_at is not UNSET:
            field_dict["origin_served_at"] = origin_served_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.environment_git_ops_plan import EnvironmentGitOpsPlan
        from ..models.environment_git_promotion_artifact import EnvironmentGitPromotionArtifact
        from ..models.environment_git_promotion_change import EnvironmentGitPromotionChange
        from ..models.environment_git_promotion_origin import EnvironmentGitPromotionOrigin
        from ..models.environment_git_promotion_target import EnvironmentGitPromotionTarget

        d = dict(src_dict)
        project = d.pop("project")

        environment = d.pop("environment")

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        read_only = d.pop("read_only")

        values_redacted = d.pop("values_redacted")

        requires_target_qualification = d.pop("requires_target_qualification")

        current_release_set_id = d.pop("current_release_set_id")

        origin = EnvironmentGitPromotionOrigin.from_dict(d.pop("origin"))

        target = EnvironmentGitPromotionTarget.from_dict(d.pop("target"))

        artifacts = []
        _artifacts = d.pop("artifacts")
        for artifacts_item_data in _artifacts:
            artifacts_item = EnvironmentGitPromotionArtifact.from_dict(artifacts_item_data)

            artifacts.append(artifacts_item)

        changes = []
        _changes = d.pop("changes")
        for changes_item_data in _changes:
            changes_item = EnvironmentGitPromotionChange.from_dict(changes_item_data)

            changes.append(changes_item)

        target_plan = EnvironmentGitOpsPlan.from_dict(d.pop("target_plan"))

        blocking_reasons = cast(list[str], d.pop("blocking_reasons"))

        plan_hash = d.pop("plan_hash")

        _origin_served_at = d.pop("origin_served_at", UNSET)
        origin_served_at: datetime.datetime | Unset
        if isinstance(_origin_served_at, Unset):
            origin_served_at = UNSET
        else:
            origin_served_at = datetime.datetime.fromisoformat(_origin_served_at)

        preview_environment_git_rollback_response = cls(
            project=project,
            environment=environment,
            observed_at=observed_at,
            read_only=read_only,
            values_redacted=values_redacted,
            requires_target_qualification=requires_target_qualification,
            current_release_set_id=current_release_set_id,
            origin=origin,
            target=target,
            artifacts=artifacts,
            changes=changes,
            target_plan=target_plan,
            blocking_reasons=blocking_reasons,
            plan_hash=plan_hash,
            origin_served_at=origin_served_at,
        )

        return preview_environment_git_rollback_response
