from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.project_environment_config_diff_response import ProjectEnvironmentConfigDiffResponse
    from ..models.project_environment_promotion_change import ProjectEnvironmentPromotionChange
    from ..models.project_release_set_response import ProjectReleaseSetResponse


T = TypeVar("T", bound="ProjectEnvironmentPromotionPreviewResponse")


@_attrs_define
class ProjectEnvironmentPromotionPreviewResponse:
    """Read-only promotion preview between two registered project environments."""

    project_slug: str
    from_environment: str
    to_environment: str
    to_environment_protected: bool
    approval_required: bool
    can_promote: bool
    config_diff: ProjectEnvironmentConfigDiffResponse
    """Stable key-level diff between two project environment configuration snapshots."""
    changes: list[ProjectEnvironmentPromotionChange]
    release_graph_mode: bool
    """True when promotion stages deployments dark and atomically activates a project release graph."""
    promotion_hash: str
    promotion_token: str
    sync_config: bool | Unset = UNSET
    """Whether this exact promotion will copy the source's non-secret configuration to the target."""
    blocking_reasons: list[str] | Unset = UNSET
    from_release_set: ProjectReleaseSetResponse | Unset = UNSET
    """Immutable project deployment graph. Active sets do not expire; when replaced, their TTL starts and
    expires_at is set."""
    to_release_set: ProjectReleaseSetResponse | Unset = UNSET
    """Immutable project deployment graph. Active sets do not expire; when replaced, their TTL starts and
    expires_at is set."""
    release_ttl_seconds: int | Unset = UNSET
    """Compatibility window used by the promoted release graph."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        project_slug = self.project_slug

        from_environment = self.from_environment

        to_environment = self.to_environment

        to_environment_protected = self.to_environment_protected

        approval_required = self.approval_required

        can_promote = self.can_promote

        config_diff = self.config_diff.to_dict()

        changes = []
        for changes_item_data in self.changes:
            changes_item = changes_item_data.to_dict()
            changes.append(changes_item)

        release_graph_mode = self.release_graph_mode

        promotion_hash = self.promotion_hash

        promotion_token = self.promotion_token

        sync_config = self.sync_config

        blocking_reasons: list[str] | Unset = UNSET
        if not isinstance(self.blocking_reasons, Unset):
            blocking_reasons = self.blocking_reasons

        from_release_set: dict[str, Any] | Unset = UNSET
        if not isinstance(self.from_release_set, Unset):
            from_release_set = self.from_release_set.to_dict()

        to_release_set: dict[str, Any] | Unset = UNSET
        if not isinstance(self.to_release_set, Unset):
            to_release_set = self.to_release_set.to_dict()

        release_ttl_seconds = self.release_ttl_seconds

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "project_slug": project_slug,
                "from_environment": from_environment,
                "to_environment": to_environment,
                "to_environment_protected": to_environment_protected,
                "approval_required": approval_required,
                "can_promote": can_promote,
                "config_diff": config_diff,
                "changes": changes,
                "release_graph_mode": release_graph_mode,
                "promotion_hash": promotion_hash,
                "promotion_token": promotion_token,
            }
        )
        if sync_config is not UNSET:
            field_dict["sync_config"] = sync_config
        if blocking_reasons is not UNSET:
            field_dict["blocking_reasons"] = blocking_reasons
        if from_release_set is not UNSET:
            field_dict["from_release_set"] = from_release_set
        if to_release_set is not UNSET:
            field_dict["to_release_set"] = to_release_set
        if release_ttl_seconds is not UNSET:
            field_dict["release_ttl_seconds"] = release_ttl_seconds

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.project_environment_config_diff_response import ProjectEnvironmentConfigDiffResponse
        from ..models.project_environment_promotion_change import ProjectEnvironmentPromotionChange
        from ..models.project_release_set_response import ProjectReleaseSetResponse

        d = dict(src_dict)
        project_slug = d.pop("project_slug")

        from_environment = d.pop("from_environment")

        to_environment = d.pop("to_environment")

        to_environment_protected = d.pop("to_environment_protected")

        approval_required = d.pop("approval_required")

        can_promote = d.pop("can_promote")

        config_diff = ProjectEnvironmentConfigDiffResponse.from_dict(d.pop("config_diff"))

        changes = []
        _changes = d.pop("changes")
        for changes_item_data in _changes:
            changes_item = ProjectEnvironmentPromotionChange.from_dict(changes_item_data)

            changes.append(changes_item)

        release_graph_mode = d.pop("release_graph_mode")

        promotion_hash = d.pop("promotion_hash")

        promotion_token = d.pop("promotion_token")

        sync_config = d.pop("sync_config", UNSET)

        blocking_reasons = cast(list[str], d.pop("blocking_reasons", UNSET))

        _from_release_set = d.pop("from_release_set", UNSET)
        from_release_set: ProjectReleaseSetResponse | Unset
        if isinstance(_from_release_set, Unset):
            from_release_set = UNSET
        else:
            from_release_set = ProjectReleaseSetResponse.from_dict(_from_release_set)

        _to_release_set = d.pop("to_release_set", UNSET)
        to_release_set: ProjectReleaseSetResponse | Unset
        if isinstance(_to_release_set, Unset):
            to_release_set = UNSET
        else:
            to_release_set = ProjectReleaseSetResponse.from_dict(_to_release_set)

        release_ttl_seconds = d.pop("release_ttl_seconds", UNSET)

        project_environment_promotion_preview_response = cls(
            project_slug=project_slug,
            from_environment=from_environment,
            to_environment=to_environment,
            to_environment_protected=to_environment_protected,
            approval_required=approval_required,
            can_promote=can_promote,
            config_diff=config_diff,
            changes=changes,
            release_graph_mode=release_graph_mode,
            promotion_hash=promotion_hash,
            promotion_token=promotion_token,
            sync_config=sync_config,
            blocking_reasons=blocking_reasons,
            from_release_set=from_release_set,
            to_release_set=to_release_set,
            release_ttl_seconds=release_ttl_seconds,
        )

        project_environment_promotion_preview_response.additional_properties = d
        return project_environment_promotion_preview_response

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
