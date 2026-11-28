from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_runtime_app_task_class_name import ObsRuntimeAppTaskClassName, check_obs_runtime_app_task_class_name
from ..models.obs_runtime_app_task_kind_name import ObsRuntimeAppTaskKindName, check_obs_runtime_app_task_kind_name

T = TypeVar("T", bound="ObsRuntimeAppTaskClassCounts")


@_attrs_define
class ObsRuntimeAppTaskClassCounts:
    """Replica-view counts for one ordered kind/class pair. These describe views of shared fleet data, not unique tasks or
    completion acknowledgements.

    """

    kind: ObsRuntimeAppTaskKindName
    """Closed durable task kind, separating release commands, manual tasks and cron tasks."""
    class_: ObsRuntimeAppTaskClassName
    """Closed durable task class. Restoring, retry_wait, held and unknown require all ages unknown. Running
    duration and held evidence do not establish an outage or bypass admission."""
    nonempty_views: int
    empty_views: int
    unknown_age_views: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        kind: str = self.kind

        class_: str = self.class_

        nonempty_views = self.nonempty_views

        empty_views = self.empty_views

        unknown_age_views = self.unknown_age_views

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "kind": kind,
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
        kind = check_obs_runtime_app_task_kind_name(d.pop("kind"))

        class_ = check_obs_runtime_app_task_class_name(d.pop("class"))

        nonempty_views = d.pop("nonempty_views")

        empty_views = d.pop("empty_views")

        unknown_age_views = d.pop("unknown_age_views")

        obs_runtime_app_task_class_counts = cls(
            kind=kind,
            class_=class_,
            nonempty_views=nonempty_views,
            empty_views=empty_views,
            unknown_age_views=unknown_age_views,
        )

        obs_runtime_app_task_class_counts.additional_properties = d
        return obs_runtime_app_task_class_counts

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
