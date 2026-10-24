from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.project_promotion_migration_policy_compatibility import (
    ProjectPromotionMigrationPolicyCompatibility,
    check_project_promotion_migration_policy_compatibility,
)

T = TypeVar("T", bound="ProjectPromotionMigrationPolicy")


@_attrs_define
class ProjectPromotionMigrationPolicy:
    """One backward-compatible source release command authorized against destination runtime inputs. Contains a command
    fingerprint, never command bytes or credentials.

    """

    app_id: UUID
    slug: str
    command_hash: str
    compatibility: ProjectPromotionMigrationPolicyCompatibility
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        slug = self.slug

        command_hash = self.command_hash

        compatibility: str = self.compatibility

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "slug": slug,
                "command_hash": command_hash,
                "compatibility": compatibility,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        slug = d.pop("slug")

        command_hash = d.pop("command_hash")

        compatibility = check_project_promotion_migration_policy_compatibility(d.pop("compatibility"))

        project_promotion_migration_policy = cls(
            app_id=app_id,
            slug=slug,
            command_hash=command_hash,
            compatibility=compatibility,
        )

        project_promotion_migration_policy.additional_properties = d
        return project_promotion_migration_policy

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
