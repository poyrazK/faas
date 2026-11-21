from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, Literal, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_serving_probe_notification_health_reason import (
    ObsServingProbeNotificationHealthReason,
    check_obs_serving_probe_notification_health_reason,
)
from ..models.obs_serving_probe_notification_health_state import (
    ObsServingProbeNotificationHealthState,
    check_obs_serving_probe_notification_health_state,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.obs_serving_probe_notification import ObsServingProbeNotification
    from ..models.obs_serving_probe_notification_counts import ObsServingProbeNotificationCounts


T = TypeVar("T", bound="ObsServingProbeNotificationHealth")


@_attrs_define
class ObsServingProbeNotificationHealth:
    """Optional, independent evidence for direct compute-node webhook and
    heartbeat submissions. Counts cover every expected node/channel; the
    bounded preview orders failures first, then unknowns, successes and
    disabled channels. Available describes evidence completeness and may
    include failed submissions. Historical success does not establish
    current reachability, provider processing or human receipt. This
    section does not change serving or deployment qualification results.

    """

    state: ObsServingProbeNotificationHealthState
    expected_channels: int | None
    counts: None | ObsServingProbeNotificationCounts
    limit: Literal[32]
    omitted: int
    truncated: bool
    channels: list[ObsServingProbeNotification]
    reason: ObsServingProbeNotificationHealthReason | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        from ..models.obs_serving_probe_notification_counts import ObsServingProbeNotificationCounts

        state: str = self.state

        expected_channels: int | None
        expected_channels = self.expected_channels

        counts: dict[str, Any] | None
        if isinstance(self.counts, ObsServingProbeNotificationCounts):
            counts = self.counts.to_dict()
        else:
            counts = self.counts

        limit = self.limit

        omitted = self.omitted

        truncated = self.truncated

        channels = []
        for channels_item_data in self.channels:
            channels_item = channels_item_data.to_dict()
            channels.append(channels_item)

        reason: str | Unset = UNSET
        if not isinstance(self.reason, Unset):
            reason = self.reason

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "state": state,
                "expected_channels": expected_channels,
                "counts": counts,
                "limit": limit,
                "omitted": omitted,
                "truncated": truncated,
                "channels": channels,
            }
        )
        if reason is not UNSET:
            field_dict["reason"] = reason

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.obs_serving_probe_notification import ObsServingProbeNotification
        from ..models.obs_serving_probe_notification_counts import ObsServingProbeNotificationCounts

        d = dict(src_dict)
        state = check_obs_serving_probe_notification_health_state(d.pop("state"))

        def _parse_expected_channels(data: object) -> int | None:
            if data is None:
                return data
            return cast(int | None, data)

        expected_channels = _parse_expected_channels(d.pop("expected_channels"))

        def _parse_counts(data: object) -> None | ObsServingProbeNotificationCounts:
            if data is None:
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                counts_type_0 = ObsServingProbeNotificationCounts.from_dict(data)

                return counts_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(None | ObsServingProbeNotificationCounts, data)

        counts = _parse_counts(d.pop("counts"))

        limit = cast(Literal[32], d.pop("limit"))
        if limit != 32:
            raise ValueError(f"limit must match const 32, got '{limit}'")

        omitted = d.pop("omitted")

        truncated = d.pop("truncated")

        channels = []
        _channels = d.pop("channels")
        for channels_item_data in _channels:
            channels_item = ObsServingProbeNotification.from_dict(channels_item_data)

            channels.append(channels_item)

        _reason = d.pop("reason", UNSET)
        reason: ObsServingProbeNotificationHealthReason | Unset
        if isinstance(_reason, Unset):
            reason = UNSET
        else:
            reason = check_obs_serving_probe_notification_health_reason(_reason)

        obs_serving_probe_notification_health = cls(
            state=state,
            expected_channels=expected_channels,
            counts=counts,
            limit=limit,
            omitted=omitted,
            truncated=truncated,
            channels=channels,
            reason=reason,
        )

        obs_serving_probe_notification_health.additional_properties = d
        return obs_serving_probe_notification_health

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
