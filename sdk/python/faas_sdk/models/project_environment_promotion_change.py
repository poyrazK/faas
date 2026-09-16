from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

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

T = TypeVar("T", bound="ProjectEnvironmentPromotionChange")


@_attrs_define
class ProjectEnvironmentPromotionChange:
    """One workload's live release identity in a source and target environment."""

    workload_slug: str
    workload_name: str
    kind: ProjectEnvironmentPromotionChangeKind
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
        d = dict(src_dict)
        workload_slug = d.pop("workload_slug")

        workload_name = d.pop("workload_name")

        kind = check_project_environment_promotion_change_kind(d.pop("kind"))

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
