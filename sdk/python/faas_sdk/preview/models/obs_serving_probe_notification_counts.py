from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ObsServingProbeNotificationCounts")


@_attrs_define
class ObsServingProbeNotificationCounts:
    """Counts include all expected node/channel pairs, including items omitted from the preview."""

    disabled: int
    unknown: int
    last_submission_succeeded: int
    last_submission_failed: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        disabled = self.disabled

        unknown = self.unknown

        last_submission_succeeded = self.last_submission_succeeded

        last_submission_failed = self.last_submission_failed

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "disabled": disabled,
                "unknown": unknown,
                "last_submission_succeeded": last_submission_succeeded,
                "last_submission_failed": last_submission_failed,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        disabled = d.pop("disabled")

        unknown = d.pop("unknown")

        last_submission_succeeded = d.pop("last_submission_succeeded")

        last_submission_failed = d.pop("last_submission_failed")

        obs_serving_probe_notification_counts = cls(
            disabled=disabled,
            unknown=unknown,
            last_submission_succeeded=last_submission_succeeded,
            last_submission_failed=last_submission_failed,
        )

        obs_serving_probe_notification_counts.additional_properties = d
        return obs_serving_probe_notification_counts

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
