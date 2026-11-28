from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_runtime_command_cron_class_name import (
    ObsRuntimeCommandCronClassName,
    check_obs_runtime_command_cron_class_name,
)

T = TypeVar("T", bound="ObsRuntimeCommandCronClassCounts")


@_attrs_define
class ObsRuntimeCommandCronClassCounts:
    """Scope counts for one ordered class across all inspected scheduler targets before clipping. Ownership scopes can
    overlap; these are not unique schedules, occurrences or successful deliveries.

    """

    class_: ObsRuntimeCommandCronClassName
    """Closed schedule observation class. Disabled, suspended, account_held, not_due and unknown require all ages
    unknown. Due, overlap_due, replacement_wait, deployment_missing and exclusive_due report nominal due evidence
    without predicting committed admission outcomes."""
    nonempty_scopes: int
    empty_scopes: int
    unknown_age_scopes: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        class_: str = self.class_

        nonempty_scopes = self.nonempty_scopes

        empty_scopes = self.empty_scopes

        unknown_age_scopes = self.unknown_age_scopes

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "class": class_,
                "nonempty_scopes": nonempty_scopes,
                "empty_scopes": empty_scopes,
                "unknown_age_scopes": unknown_age_scopes,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        class_ = check_obs_runtime_command_cron_class_name(d.pop("class"))

        nonempty_scopes = d.pop("nonempty_scopes")

        empty_scopes = d.pop("empty_scopes")

        unknown_age_scopes = d.pop("unknown_age_scopes")

        obs_runtime_command_cron_class_counts = cls(
            class_=class_,
            nonempty_scopes=nonempty_scopes,
            empty_scopes=empty_scopes,
            unknown_age_scopes=unknown_age_scopes,
        )

        obs_runtime_command_cron_class_counts.additional_properties = d
        return obs_runtime_command_cron_class_counts

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
