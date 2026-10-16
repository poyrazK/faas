from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.environment_git_ops_plan import EnvironmentGitOpsPlan
    from ..models.environment_git_promotion_artifact import EnvironmentGitPromotionArtifact
    from ..models.environment_git_promotion_change import EnvironmentGitPromotionChange
    from ..models.environment_git_promotion_origin import EnvironmentGitPromotionOrigin
    from ..models.environment_git_promotion_target import EnvironmentGitPromotionTarget


T = TypeVar("T", bound="PreviewEnvironmentGitPromotionResponse")


@_attrs_define
class PreviewEnvironmentGitPromotionResponse:
    """Read-only, value-redacted promotion proposal. It neither activates a release nor proves target readiness."""

    project: str
    """Project slug."""
    from_environment: str
    """Source environment slug."""
    to_environment: str
    """Target environment slug."""
    observed_at: datetime.datetime
    """Observation time."""
    read_only: bool
    """Always true; no writes or leases are issued."""
    values_redacted: bool
    """Configuration values and secret-reference targets are omitted."""
    requires_target_qualification: bool
    """Source evidence cannot authorize target activation."""
    origin: EnvironmentGitPromotionOrigin
    """Scoped identity of the approved source release; graph and release IDs are empty if unproven."""
    target: EnvironmentGitPromotionTarget
    """Target binding and manifest observation; no target values are returned."""
    artifacts: list[EnvironmentGitPromotionArtifact]
    """Selected source deployment identities."""
    changes: list[EnvironmentGitPromotionChange]
    """Image-only source contract proposals."""
    target_plan: EnvironmentGitOpsPlan
    """Observation-bound plan; blocking reasons prevent mutation and overrides prevent convergence."""
    blocking_reasons: list[str]
    """Reasons patch or manifest export is unavailable."""
    plan_hash: str
    """Domain-separated hash of source release, artifacts and target observation; never an execution token."""

    def to_dict(self) -> dict[str, Any]:
        project = self.project

        from_environment = self.from_environment

        to_environment = self.to_environment

        observed_at = self.observed_at.isoformat()

        read_only = self.read_only

        values_redacted = self.values_redacted

        requires_target_qualification = self.requires_target_qualification

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

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "project": project,
                "from_environment": from_environment,
                "to_environment": to_environment,
                "observed_at": observed_at,
                "read_only": read_only,
                "values_redacted": values_redacted,
                "requires_target_qualification": requires_target_qualification,
                "origin": origin,
                "target": target,
                "artifacts": artifacts,
                "changes": changes,
                "target_plan": target_plan,
                "blocking_reasons": blocking_reasons,
                "plan_hash": plan_hash,
            }
        )

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

        from_environment = d.pop("from_environment")

        to_environment = d.pop("to_environment")

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        read_only = d.pop("read_only")

        values_redacted = d.pop("values_redacted")

        requires_target_qualification = d.pop("requires_target_qualification")

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

        preview_environment_git_promotion_response = cls(
            project=project,
            from_environment=from_environment,
            to_environment=to_environment,
            observed_at=observed_at,
            read_only=read_only,
            values_redacted=values_redacted,
            requires_target_qualification=requires_target_qualification,
            origin=origin,
            target=target,
            artifacts=artifacts,
            changes=changes,
            target_plan=target_plan,
            blocking_reasons=blocking_reasons,
            plan_hash=plan_hash,
        )

        return preview_environment_git_promotion_response
