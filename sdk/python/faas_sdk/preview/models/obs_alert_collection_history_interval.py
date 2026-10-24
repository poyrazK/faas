from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_alert_collection_history_interval_reason import (
    ObsAlertCollectionHistoryIntervalReason,
    check_obs_alert_collection_history_interval_reason,
)
from ..models.obs_alert_collection_history_interval_state import (
    ObsAlertCollectionHistoryIntervalState,
    check_obs_alert_collection_history_interval_state,
)

T = TypeVar("T", bound="ObsAlertCollectionHistoryInterval")


@_attrs_define
class ObsAlertCollectionHistoryInterval:
    """Half-open coalesced interval of fresh outcome evidence or unknown observation coverage."""

    from_: datetime.datetime
    until: datetime.datetime
    state: ObsAlertCollectionHistoryIntervalState
    reason: ObsAlertCollectionHistoryIntervalReason
    """Empty on successful observations; failures retain only closed codes; unknown intervals have
    no_recent_outcome without an inferred cause."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        from_ = self.from_.isoformat()

        until = self.until.isoformat()

        state: str = self.state

        reason: str = self.reason

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "from": from_,
                "until": until,
                "state": state,
                "reason": reason,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        from_ = datetime.datetime.fromisoformat(d.pop("from"))

        until = datetime.datetime.fromisoformat(d.pop("until"))

        state = check_obs_alert_collection_history_interval_state(d.pop("state"))

        reason = check_obs_alert_collection_history_interval_reason(d.pop("reason"))

        obs_alert_collection_history_interval = cls(
            from_=from_,
            until=until,
            state=state,
            reason=reason,
        )

        obs_alert_collection_history_interval.additional_properties = d
        return obs_alert_collection_history_interval

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
