from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.project_environment_config_change import ProjectEnvironmentConfigChange


T = TypeVar("T", bound="ProjectEnvironmentConfigDiffResponse")


@_attrs_define
class ProjectEnvironmentConfigDiffResponse:
    """Stable key-level diff between two project environment configuration snapshots."""

    project_slug: str
    from_environment: str
    to_environment: str
    from_version: int
    to_version: int
    from_hash: str
    to_hash: str
    changes: list[ProjectEnvironmentConfigChange]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        project_slug = self.project_slug

        from_environment = self.from_environment

        to_environment = self.to_environment

        from_version = self.from_version

        to_version = self.to_version

        from_hash = self.from_hash

        to_hash = self.to_hash

        changes = []
        for changes_item_data in self.changes:
            changes_item = changes_item_data.to_dict()
            changes.append(changes_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "project_slug": project_slug,
                "from_environment": from_environment,
                "to_environment": to_environment,
                "from_version": from_version,
                "to_version": to_version,
                "from_hash": from_hash,
                "to_hash": to_hash,
                "changes": changes,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.project_environment_config_change import ProjectEnvironmentConfigChange

        d = dict(src_dict)
        project_slug = d.pop("project_slug")

        from_environment = d.pop("from_environment")

        to_environment = d.pop("to_environment")

        from_version = d.pop("from_version")

        to_version = d.pop("to_version")

        from_hash = d.pop("from_hash")

        to_hash = d.pop("to_hash")

        changes = []
        _changes = d.pop("changes")
        for changes_item_data in _changes:
            changes_item = ProjectEnvironmentConfigChange.from_dict(changes_item_data)

            changes.append(changes_item)

        project_environment_config_diff_response = cls(
            project_slug=project_slug,
            from_environment=from_environment,
            to_environment=to_environment,
            from_version=from_version,
            to_version=to_version,
            from_hash=from_hash,
            to_hash=to_hash,
            changes=changes,
        )

        project_environment_config_diff_response.additional_properties = d
        return project_environment_config_diff_response

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
