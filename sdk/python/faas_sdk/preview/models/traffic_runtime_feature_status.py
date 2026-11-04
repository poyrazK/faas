from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.traffic_runtime_feature_status_mode import (
    TrafficRuntimeFeatureStatusMode,
    check_traffic_runtime_feature_status_mode,
)
from ..models.traffic_runtime_feature_status_state import (
    TrafficRuntimeFeatureStatusState,
    check_traffic_runtime_feature_status_state,
)

T = TypeVar("T", bound="TrafficRuntimeFeatureStatus")


@_attrs_define
class TrafficRuntimeFeatureStatus:
    """Observed wiring mode when every serving member has a fresh report. Disagreement reports mixed, including shared
    retry backend endpoint disagreement. Missing or stale members leave the feature unverified.

    """

    state: TrafficRuntimeFeatureStatusState
    mode: TrafficRuntimeFeatureStatusMode
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        state: str = self.state

        mode: str = self.mode

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "state": state,
                "mode": mode,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        state = check_traffic_runtime_feature_status_state(d.pop("state"))

        mode = check_traffic_runtime_feature_status_mode(d.pop("mode"))

        traffic_runtime_feature_status = cls(
            state=state,
            mode=mode,
        )

        traffic_runtime_feature_status.additional_properties = d
        return traffic_runtime_feature_status

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
