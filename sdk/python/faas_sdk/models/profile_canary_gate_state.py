from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.profile_canary_gate_state_status import (
    ProfileCanaryGateStateStatus,
    check_profile_canary_gate_state_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.profile_canary_gate_policy import ProfileCanaryGatePolicy
    from ..models.profile_gate_route_streak import ProfileGateRouteStreak
    from ..models.profile_query import ProfileQuery


T = TypeVar("T", bound="ProfileCanaryGateState")


@_attrs_define
class ProfileCanaryGateState:
    """Retained stage evidence and bounded route streaks used to qualify the profiling gate."""

    policy: ProfileCanaryGatePolicy
    """Opt-in policy requiring consecutive qualified route CPU/request comparisons before a canary stage advances."""
    status: ProfileCanaryGateStateStatus
    reason: str
    deadline: datetime.datetime
    windows: int
    streaks: list[ProfileGateRouteStreak]
    last_window_end: datetime.datetime | Unset = UNSET
    next_candidate: ProfileQuery | Unset = UNSET
    """Authorized deployment CPU capture window."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        policy = self.policy.to_dict()

        status: str = self.status

        reason = self.reason

        deadline = self.deadline.isoformat()

        windows = self.windows

        streaks = []
        for streaks_item_data in self.streaks:
            streaks_item = streaks_item_data.to_dict()
            streaks.append(streaks_item)

        last_window_end: str | Unset = UNSET
        if not isinstance(self.last_window_end, Unset):
            last_window_end = self.last_window_end.isoformat()

        next_candidate: dict[str, Any] | Unset = UNSET
        if not isinstance(self.next_candidate, Unset):
            next_candidate = self.next_candidate.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "policy": policy,
                "status": status,
                "reason": reason,
                "deadline": deadline,
                "windows": windows,
                "streaks": streaks,
            }
        )
        if last_window_end is not UNSET:
            field_dict["last_window_end"] = last_window_end
        if next_candidate is not UNSET:
            field_dict["next_candidate"] = next_candidate

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.profile_canary_gate_policy import ProfileCanaryGatePolicy
        from ..models.profile_gate_route_streak import ProfileGateRouteStreak
        from ..models.profile_query import ProfileQuery

        d = dict(src_dict)
        policy = ProfileCanaryGatePolicy.from_dict(d.pop("policy"))

        status = check_profile_canary_gate_state_status(d.pop("status"))

        reason = d.pop("reason")

        deadline = datetime.datetime.fromisoformat(d.pop("deadline"))

        windows = d.pop("windows")

        streaks = []
        _streaks = d.pop("streaks")
        for streaks_item_data in _streaks:
            streaks_item = ProfileGateRouteStreak.from_dict(streaks_item_data)

            streaks.append(streaks_item)

        _last_window_end = d.pop("last_window_end", UNSET)
        last_window_end: datetime.datetime | Unset
        if isinstance(_last_window_end, Unset):
            last_window_end = UNSET
        else:
            last_window_end = datetime.datetime.fromisoformat(_last_window_end)

        _next_candidate = d.pop("next_candidate", UNSET)
        next_candidate: ProfileQuery | Unset
        if isinstance(_next_candidate, Unset):
            next_candidate = UNSET
        else:
            next_candidate = ProfileQuery.from_dict(_next_candidate)

        profile_canary_gate_state = cls(
            policy=policy,
            status=status,
            reason=reason,
            deadline=deadline,
            windows=windows,
            streaks=streaks,
            last_window_end=last_window_end,
            next_candidate=next_candidate,
        )

        profile_canary_gate_state.additional_properties = d
        return profile_canary_gate_state

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
