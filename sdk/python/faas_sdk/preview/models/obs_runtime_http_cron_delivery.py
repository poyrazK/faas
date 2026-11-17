from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_runtime_http_cron_delivery_reason import (
    ObsRuntimeHTTPCronDeliveryReason,
    check_obs_runtime_http_cron_delivery_reason,
)
from ..models.obs_runtime_http_cron_delivery_scope import (
    ObsRuntimeHTTPCronDeliveryScope,
    check_obs_runtime_http_cron_delivery_scope,
)
from ..models.obs_runtime_http_cron_delivery_state import (
    ObsRuntimeHTTPCronDeliveryState,
    check_obs_runtime_http_cron_delivery_state,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.obs_runtime_http_cron_delivery_counts import ObsRuntimeHTTPCronDeliveryCounts
    from ..models.obs_runtime_http_cron_delivery_item import ObsRuntimeHTTPCronDeliveryItem
    from ..models.obs_runtime_http_cron_delivery_outcome_summary import ObsRuntimeHTTPCronDeliveryOutcomeSummary


T = TypeVar("T", bound="ObsRuntimeHTTPCronDelivery")


@_attrs_define
class ObsRuntimeHTTPCronDelivery:
    """Advisory settled HTTP cron delivery attempts for v2 scheduler jobs, including scheduled fires and manual runs. The
    five ordered outcomes are success, retry, app_error, failed and dead_letter. Application errors remain separate from
    producer-classified delivery failures. Retry does not establish its cause or final result, and success does not
    prove unique or exactly-once execution. Exact counter differences require fresh original sample clocks, unchanged
    sampled process proof and coherent uptime advance within the existing one-second tolerance. Baselines, resets,
    process changes and incomplete evidence establish no delta. No deployment gate or failure-rate threshold is
    inferred.

    """

    scope: ObsRuntimeHTTPCronDeliveryScope
    state: ObsRuntimeHTTPCronDeliveryState
    counts: ObsRuntimeHTTPCronDeliveryCounts
    """Full-set coverage before preview clipping, with five counter signals per scheduler target. Increased,
    unchanged, baseline, missing, stale, conflicting and unavailable partition inspected signals. Observed counts
    usable current points, including unknown comparisons. These are signal classifications, not delta magnitudes,
    unique schedules or deliveries."""
    outcomes: list[ObsRuntimeHTTPCronDeliveryOutcomeSummary]
    incomplete: bool
    items: list[ObsRuntimeHTTPCronDeliveryItem]
    omitted_items: int
    reason: ObsRuntimeHTTPCronDeliveryReason | Unset = UNSET
    comparison_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        scope: str = self.scope

        state: str = self.state

        counts = self.counts.to_dict()

        outcomes = []
        for outcomes_item_data in self.outcomes:
            outcomes_item = outcomes_item_data.to_dict()
            outcomes.append(outcomes_item)

        incomplete = self.incomplete

        items = []
        for items_item_data in self.items:
            items_item = items_item_data.to_dict()
            items.append(items_item)

        omitted_items = self.omitted_items

        reason: str | Unset = UNSET
        if not isinstance(self.reason, Unset):
            reason = self.reason

        comparison_at: str | Unset = UNSET
        if not isinstance(self.comparison_at, Unset):
            comparison_at = self.comparison_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "scope": scope,
                "state": state,
                "counts": counts,
                "outcomes": outcomes,
                "incomplete": incomplete,
                "items": items,
                "omitted_items": omitted_items,
            }
        )
        if reason is not UNSET:
            field_dict["reason"] = reason
        if comparison_at is not UNSET:
            field_dict["comparison_at"] = comparison_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.obs_runtime_http_cron_delivery_counts import ObsRuntimeHTTPCronDeliveryCounts
        from ..models.obs_runtime_http_cron_delivery_item import ObsRuntimeHTTPCronDeliveryItem
        from ..models.obs_runtime_http_cron_delivery_outcome_summary import ObsRuntimeHTTPCronDeliveryOutcomeSummary

        d = dict(src_dict)
        scope = check_obs_runtime_http_cron_delivery_scope(d.pop("scope"))

        state = check_obs_runtime_http_cron_delivery_state(d.pop("state"))

        counts = ObsRuntimeHTTPCronDeliveryCounts.from_dict(d.pop("counts"))

        outcomes = []
        _outcomes = d.pop("outcomes")
        for outcomes_item_data in _outcomes:
            outcomes_item = ObsRuntimeHTTPCronDeliveryOutcomeSummary.from_dict(outcomes_item_data)

            outcomes.append(outcomes_item)

        incomplete = d.pop("incomplete")

        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = ObsRuntimeHTTPCronDeliveryItem.from_dict(items_item_data)

            items.append(items_item)

        omitted_items = d.pop("omitted_items")

        _reason = d.pop("reason", UNSET)
        reason: ObsRuntimeHTTPCronDeliveryReason | Unset
        if isinstance(_reason, Unset):
            reason = UNSET
        else:
            reason = check_obs_runtime_http_cron_delivery_reason(_reason)

        _comparison_at = d.pop("comparison_at", UNSET)
        comparison_at: datetime.datetime | Unset
        if isinstance(_comparison_at, Unset):
            comparison_at = UNSET
        else:
            comparison_at = datetime.datetime.fromisoformat(_comparison_at)

        obs_runtime_http_cron_delivery = cls(
            scope=scope,
            state=state,
            counts=counts,
            outcomes=outcomes,
            incomplete=incomplete,
            items=items,
            omitted_items=omitted_items,
            reason=reason,
            comparison_at=comparison_at,
        )

        obs_runtime_http_cron_delivery.additional_properties = d
        return obs_runtime_http_cron_delivery

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
