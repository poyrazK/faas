from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.feature_flag import FeatureFlag
    from ..models.flags_config_groups import FlagsConfigGroups


T = TypeVar("T", bound="FeatureFlagVersion")


@_attrs_define
class FeatureFlagVersion:
    """Configuration publication with audit metadata; version zero is an unpublished empty environment."""

    flags: list[FeatureFlag]
    groups: FlagsConfigGroups
    environment_id: UUID
    version: int
    actor: str
    """Publishing account identity."""
    created_at: datetime.datetime
    restored_from: int | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        flags = []
        for flags_item_data in self.flags:
            flags_item = flags_item_data.to_dict()
            flags.append(flags_item)

        groups = self.groups.to_dict()

        environment_id = str(self.environment_id)

        version = self.version

        actor = self.actor

        created_at = self.created_at.isoformat()

        restored_from = self.restored_from

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "flags": flags,
                "groups": groups,
                "environment_id": environment_id,
                "version": version,
                "actor": actor,
                "created_at": created_at,
            }
        )
        if restored_from is not UNSET:
            field_dict["restored_from"] = restored_from

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.feature_flag import FeatureFlag
        from ..models.flags_config_groups import FlagsConfigGroups

        d = dict(src_dict)
        flags = []
        _flags = d.pop("flags")
        for flags_item_data in _flags:
            flags_item = FeatureFlag.from_dict(flags_item_data)

            flags.append(flags_item)

        groups = FlagsConfigGroups.from_dict(d.pop("groups"))

        environment_id = UUID(d.pop("environment_id"))

        version = d.pop("version")

        actor = d.pop("actor")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        restored_from = d.pop("restored_from", UNSET)

        feature_flag_version = cls(
            flags=flags,
            groups=groups,
            environment_id=environment_id,
            version=version,
            actor=actor,
            created_at=created_at,
            restored_from=restored_from,
        )

        feature_flag_version.additional_properties = d
        return feature_flag_version

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
