from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.canary_preset_spec import CanaryPresetSpec


T = TypeVar("T", bound="ScheduleReleaseRequest")


@_attrs_define
class ScheduleReleaseRequest:
    """When and how a release waiting without traffic starts its rollout."""

    release_at: datetime.datetime
    """When the rollout starts; at most 7 days ahead. A time that has passed starts it on the API's next pass."""
    canary: CanaryPresetSpec | Unset = UNSET
    """The canary ladder a customer asks for on a deploy (A + production-leveling
    Stream F). Preset is the catalog name (none/slow/balanced/aggressive/1-10-50-100/adaptive/
    custom). When Preset is 'custom', Stages is the
    customer-supplied ladder (each entry is percent +
    duration string in time.ParseDuration form, e.g.
    "1% at 30s, 10% at 2m, 100% at 0s").
    The wire-format change (StepDurations removed, Stages
    added) is additive on the consumer side because the
    prior StepDurations field was declared-but-dead — no
    pre-PR client ever sent it.
    """
    notify_channels: list[str] | Unset = UNSET
    override_freeze: str | Unset = UNSET
    """Release into the deploy freezes active at release_at; audited when the release starts."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        release_at = self.release_at.isoformat()

        canary: dict[str, Any] | Unset = UNSET
        if not isinstance(self.canary, Unset):
            canary = self.canary.to_dict()

        notify_channels: list[str] | Unset = UNSET
        if not isinstance(self.notify_channels, Unset):
            notify_channels = self.notify_channels

        override_freeze = self.override_freeze

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "release_at": release_at,
            }
        )
        if canary is not UNSET:
            field_dict["canary"] = canary
        if notify_channels is not UNSET:
            field_dict["notify_channels"] = notify_channels
        if override_freeze is not UNSET:
            field_dict["override_freeze"] = override_freeze

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.canary_preset_spec import CanaryPresetSpec

        d = dict(src_dict)
        release_at = datetime.datetime.fromisoformat(d.pop("release_at"))

        _canary = d.pop("canary", UNSET)
        canary: CanaryPresetSpec | Unset
        if isinstance(_canary, Unset):
            canary = UNSET
        else:
            canary = CanaryPresetSpec.from_dict(_canary)

        notify_channels = cast(list[str], d.pop("notify_channels", UNSET))

        override_freeze = d.pop("override_freeze", UNSET)

        schedule_release_request = cls(
            release_at=release_at,
            canary=canary,
            notify_channels=notify_channels,
            override_freeze=override_freeze,
        )

        schedule_release_request.additional_properties = d
        return schedule_release_request

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
