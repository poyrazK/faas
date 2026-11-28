from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_runtime_post_capture_class_name import (
    ObsRuntimePostCaptureClassName,
    check_obs_runtime_post_capture_class_name,
)

T = TypeVar("T", bound="ObsRuntimePostCaptureClassCounts")


@_attrs_define
class ObsRuntimePostCaptureClassCounts:
    """Replica-view counts for one ordered class over all inspected targets before clipping. Views share fleet data; these
    are neither unique deployment counts nor completion counters.

    """

    class_: ObsRuntimePostCaptureClassName
    """Closed durable post-capture evidence class. Holds and overdue gates describe retained dependency,
    compatibility or verification evidence. They do not independently determine present deployment eligibility.
    Unknown class counts always have unknown ages."""
    nonempty_views: int
    empty_views: int
    unknown_age_views: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        class_: str = self.class_

        nonempty_views = self.nonempty_views

        empty_views = self.empty_views

        unknown_age_views = self.unknown_age_views

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "class": class_,
                "nonempty_views": nonempty_views,
                "empty_views": empty_views,
                "unknown_age_views": unknown_age_views,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        class_ = check_obs_runtime_post_capture_class_name(d.pop("class"))

        nonempty_views = d.pop("nonempty_views")

        empty_views = d.pop("empty_views")

        unknown_age_views = d.pop("unknown_age_views")

        obs_runtime_post_capture_class_counts = cls(
            class_=class_,
            nonempty_views=nonempty_views,
            empty_views=empty_views,
            unknown_age_views=unknown_age_views,
        )

        obs_runtime_post_capture_class_counts.additional_properties = d
        return obs_runtime_post_capture_class_counts

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
