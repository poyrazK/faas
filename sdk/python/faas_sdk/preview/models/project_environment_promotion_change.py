from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.project_environment_promotion_change_kind import (
    ProjectEnvironmentPromotionChangeKind,
    check_project_environment_promotion_change_kind,
)
from ..models.project_environment_promotion_change_source_revision_kind import (
    ProjectEnvironmentPromotionChangeSourceRevisionKind,
    check_project_environment_promotion_change_source_revision_kind,
)
from ..models.project_environment_promotion_change_target_revision_kind import (
    ProjectEnvironmentPromotionChangeTargetRevisionKind,
    check_project_environment_promotion_change_target_revision_kind,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.project_environment_config_change import ProjectEnvironmentConfigChange


T = TypeVar("T", bound="ProjectEnvironmentPromotionChange")


@_attrs_define
class ProjectEnvironmentPromotionChange:
    """One workload's live release identity in a source and target environment."""

    workload_slug: str
    workload_name: str
    kind: ProjectEnvironmentPromotionChangeKind
    secret_changes: list[ProjectEnvironmentConfigChange] | Unset = UNSET
    """Production secret key names and change kinds, without values or fingerprints."""
    release_configuration_hash: str | Unset = UNSET
    """Identity of the approved immutable HTTP variable snapshot and environment versions; destination secrets and
    bindings remain owned by the target."""
    environment_changes: list[ProjectEnvironmentConfigChange] | Unset = UNSET
    """Variable key names and change kinds only; values and secret material are omitted."""
    source_workload_config_hash: str | Unset = UNSET
    target_workload_config_hash: str | Unset = UNSET
    promoted_workload_config_hash: str | Unset = UNSET
    source_deployment_id: str | Unset = UNSET
    target_deployment_id: str | Unset = UNSET
    source_build_id: str | Unset = UNSET
    target_build_id: str | Unset = UNSET
    source_revision: str | Unset = UNSET
    target_revision: str | Unset = UNSET
    source_revision_kind: ProjectEnvironmentPromotionChangeSourceRevisionKind | Unset = UNSET
    target_revision_kind: ProjectEnvironmentPromotionChangeTargetRevisionKind | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        workload_slug = self.workload_slug

        workload_name = self.workload_name

        kind: str = self.kind

        secret_changes: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.secret_changes, Unset):
            secret_changes = []
            for secret_changes_item_data in self.secret_changes:
                secret_changes_item = secret_changes_item_data.to_dict()
                secret_changes.append(secret_changes_item)

        release_configuration_hash = self.release_configuration_hash

        environment_changes: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.environment_changes, Unset):
            environment_changes = []
            for environment_changes_item_data in self.environment_changes:
                environment_changes_item = environment_changes_item_data.to_dict()
                environment_changes.append(environment_changes_item)

        source_workload_config_hash = self.source_workload_config_hash

        target_workload_config_hash = self.target_workload_config_hash

        promoted_workload_config_hash = self.promoted_workload_config_hash

        source_deployment_id = self.source_deployment_id

        target_deployment_id = self.target_deployment_id

        source_build_id = self.source_build_id

        target_build_id = self.target_build_id

        source_revision = self.source_revision

        target_revision = self.target_revision

        source_revision_kind: str | Unset = UNSET
        if not isinstance(self.source_revision_kind, Unset):
            source_revision_kind = self.source_revision_kind

        target_revision_kind: str | Unset = UNSET
        if not isinstance(self.target_revision_kind, Unset):
            target_revision_kind = self.target_revision_kind

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "workload_slug": workload_slug,
                "workload_name": workload_name,
                "kind": kind,
            }
        )
        if secret_changes is not UNSET:
            field_dict["secret_changes"] = secret_changes
        if release_configuration_hash is not UNSET:
            field_dict["release_configuration_hash"] = release_configuration_hash
        if environment_changes is not UNSET:
            field_dict["environment_changes"] = environment_changes
        if source_workload_config_hash is not UNSET:
            field_dict["source_workload_config_hash"] = source_workload_config_hash
        if target_workload_config_hash is not UNSET:
            field_dict["target_workload_config_hash"] = target_workload_config_hash
        if promoted_workload_config_hash is not UNSET:
            field_dict["promoted_workload_config_hash"] = promoted_workload_config_hash
        if source_deployment_id is not UNSET:
            field_dict["source_deployment_id"] = source_deployment_id
        if target_deployment_id is not UNSET:
            field_dict["target_deployment_id"] = target_deployment_id
        if source_build_id is not UNSET:
            field_dict["source_build_id"] = source_build_id
        if target_build_id is not UNSET:
            field_dict["target_build_id"] = target_build_id
        if source_revision is not UNSET:
            field_dict["source_revision"] = source_revision
        if target_revision is not UNSET:
            field_dict["target_revision"] = target_revision
        if source_revision_kind is not UNSET:
            field_dict["source_revision_kind"] = source_revision_kind
        if target_revision_kind is not UNSET:
            field_dict["target_revision_kind"] = target_revision_kind

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.project_environment_config_change import ProjectEnvironmentConfigChange

        d = dict(src_dict)
        workload_slug = d.pop("workload_slug")

        workload_name = d.pop("workload_name")

        kind = check_project_environment_promotion_change_kind(d.pop("kind"))

        _secret_changes = d.pop("secret_changes", UNSET)
        secret_changes: list[ProjectEnvironmentConfigChange] | Unset = UNSET
        if _secret_changes is not UNSET:
            secret_changes = []
            for secret_changes_item_data in _secret_changes:
                secret_changes_item = ProjectEnvironmentConfigChange.from_dict(secret_changes_item_data)

                secret_changes.append(secret_changes_item)

        release_configuration_hash = d.pop("release_configuration_hash", UNSET)

        _environment_changes = d.pop("environment_changes", UNSET)
        environment_changes: list[ProjectEnvironmentConfigChange] | Unset = UNSET
        if _environment_changes is not UNSET:
            environment_changes = []
            for environment_changes_item_data in _environment_changes:
                environment_changes_item = ProjectEnvironmentConfigChange.from_dict(environment_changes_item_data)

                environment_changes.append(environment_changes_item)

        source_workload_config_hash = d.pop("source_workload_config_hash", UNSET)

        target_workload_config_hash = d.pop("target_workload_config_hash", UNSET)

        promoted_workload_config_hash = d.pop("promoted_workload_config_hash", UNSET)

        source_deployment_id = d.pop("source_deployment_id", UNSET)

        target_deployment_id = d.pop("target_deployment_id", UNSET)

        source_build_id = d.pop("source_build_id", UNSET)

        target_build_id = d.pop("target_build_id", UNSET)

        source_revision = d.pop("source_revision", UNSET)

        target_revision = d.pop("target_revision", UNSET)

        _source_revision_kind = d.pop("source_revision_kind", UNSET)
        source_revision_kind: ProjectEnvironmentPromotionChangeSourceRevisionKind | Unset
        if isinstance(_source_revision_kind, Unset):
            source_revision_kind = UNSET
        else:
            source_revision_kind = check_project_environment_promotion_change_source_revision_kind(
                _source_revision_kind
            )

        _target_revision_kind = d.pop("target_revision_kind", UNSET)
        target_revision_kind: ProjectEnvironmentPromotionChangeTargetRevisionKind | Unset
        if isinstance(_target_revision_kind, Unset):
            target_revision_kind = UNSET
        else:
            target_revision_kind = check_project_environment_promotion_change_target_revision_kind(
                _target_revision_kind
            )

        project_environment_promotion_change = cls(
            workload_slug=workload_slug,
            workload_name=workload_name,
            kind=kind,
            secret_changes=secret_changes,
            release_configuration_hash=release_configuration_hash,
            environment_changes=environment_changes,
            source_workload_config_hash=source_workload_config_hash,
            target_workload_config_hash=target_workload_config_hash,
            promoted_workload_config_hash=promoted_workload_config_hash,
            source_deployment_id=source_deployment_id,
            target_deployment_id=target_deployment_id,
            source_build_id=source_build_id,
            target_build_id=target_build_id,
            source_revision=source_revision,
            target_revision=target_revision,
            source_revision_kind=source_revision_kind,
            target_revision_kind=target_revision_kind,
        )

        project_environment_promotion_change.additional_properties = d
        return project_environment_promotion_change

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
