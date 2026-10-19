from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.checked_project_release_manifest_deployments import CheckedProjectReleaseManifestDeployments
    from ..models.checked_project_release_manifest_expected_fallback_deployments import (
        CheckedProjectReleaseManifestExpectedFallbackDeployments,
    )
    from ..models.checked_project_release_manifest_expected_policy_revisions import (
        CheckedProjectReleaseManifestExpectedPolicyRevisions,
    )
    from ..models.checked_project_release_manifest_service_targets import CheckedProjectReleaseManifestServiceTargets


T = TypeVar("T", bound="CheckedProjectReleaseManifest")


@_attrs_define
class CheckedProjectReleaseManifest:
    """Immutable candidate graph and expected routing and policy state. This manifest conveys no evidence or waiver."""

    project: str
    environment: str
    ttl_seconds: int
    """Zero for deactivation; positive retention TTL for activation or rollback."""
    deployments: CheckedProjectReleaseManifestDeployments
    expected_active_release_id: str
    """Expected active release UUID or an empty string for the first graph."""
    expected_fallback_deployments: CheckedProjectReleaseManifestExpectedFallbackDeployments
    expected_policy_revisions: CheckedProjectReleaseManifestExpectedPolicyRevisions
    service_targets: CheckedProjectReleaseManifestServiceTargets
    deactivate: bool | Unset = UNSET
    """Explicit intent to return to checked weighted fallback routes; requires the deactivation endpoint."""
    rollback_release_id: UUID | Unset = UNSET
    """Historical source identity; requires the checked rollback endpoint."""

    def to_dict(self) -> dict[str, Any]:
        project = self.project

        environment = self.environment

        ttl_seconds = self.ttl_seconds

        deployments = self.deployments.to_dict()

        expected_active_release_id = self.expected_active_release_id

        expected_fallback_deployments = self.expected_fallback_deployments.to_dict()

        expected_policy_revisions = self.expected_policy_revisions.to_dict()

        service_targets = self.service_targets.to_dict()

        deactivate = self.deactivate

        rollback_release_id: str | Unset = UNSET
        if not isinstance(self.rollback_release_id, Unset):
            rollback_release_id = str(self.rollback_release_id)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "project": project,
                "environment": environment,
                "ttl_seconds": ttl_seconds,
                "deployments": deployments,
                "expected_active_release_id": expected_active_release_id,
                "expected_fallback_deployments": expected_fallback_deployments,
                "expected_policy_revisions": expected_policy_revisions,
                "service_targets": service_targets,
            }
        )
        if deactivate is not UNSET:
            field_dict["deactivate"] = deactivate
        if rollback_release_id is not UNSET:
            field_dict["rollback_release_id"] = rollback_release_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.checked_project_release_manifest_deployments import CheckedProjectReleaseManifestDeployments
        from ..models.checked_project_release_manifest_expected_fallback_deployments import (
            CheckedProjectReleaseManifestExpectedFallbackDeployments,
        )
        from ..models.checked_project_release_manifest_expected_policy_revisions import (
            CheckedProjectReleaseManifestExpectedPolicyRevisions,
        )
        from ..models.checked_project_release_manifest_service_targets import (
            CheckedProjectReleaseManifestServiceTargets,
        )

        d = dict(src_dict)
        project = d.pop("project")

        environment = d.pop("environment")

        ttl_seconds = d.pop("ttl_seconds")

        deployments = CheckedProjectReleaseManifestDeployments.from_dict(d.pop("deployments"))

        expected_active_release_id = d.pop("expected_active_release_id")

        expected_fallback_deployments = CheckedProjectReleaseManifestExpectedFallbackDeployments.from_dict(
            d.pop("expected_fallback_deployments")
        )

        expected_policy_revisions = CheckedProjectReleaseManifestExpectedPolicyRevisions.from_dict(
            d.pop("expected_policy_revisions")
        )

        service_targets = CheckedProjectReleaseManifestServiceTargets.from_dict(d.pop("service_targets"))

        deactivate = d.pop("deactivate", UNSET)

        _rollback_release_id = d.pop("rollback_release_id", UNSET)
        rollback_release_id: UUID | Unset
        if isinstance(_rollback_release_id, Unset):
            rollback_release_id = UNSET
        else:
            rollback_release_id = UUID(_rollback_release_id)

        checked_project_release_manifest = cls(
            project=project,
            environment=environment,
            ttl_seconds=ttl_seconds,
            deployments=deployments,
            expected_active_release_id=expected_active_release_id,
            expected_fallback_deployments=expected_fallback_deployments,
            expected_policy_revisions=expected_policy_revisions,
            service_targets=service_targets,
            deactivate=deactivate,
            rollback_release_id=rollback_release_id,
        )

        return checked_project_release_manifest
