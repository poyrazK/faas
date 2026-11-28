from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_runtime_app_task_operation_reason import (
    ObsRuntimeAppTaskOperationReason,
    check_obs_runtime_app_task_operation_reason,
)
from ..models.obs_runtime_http_cron_delivery_name import (
    ObsRuntimeHTTPCronDeliveryName,
    check_obs_runtime_http_cron_delivery_name,
)
from ..models.obs_runtime_http_cron_delivery_signal_state import (
    ObsRuntimeHTTPCronDeliverySignalState,
    check_obs_runtime_http_cron_delivery_signal_state,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.obs_runtime_app_task_operation_point import ObsRuntimeAppTaskOperationPoint


T = TypeVar("T", bound="ObsRuntimeHTTPCronDeliverySignal")


@_attrs_define
class ObsRuntimeHTTPCronDeliverySignal:
    """One process-local HTTP delivery outcome counter. First usable points are baselines. Comparable points retain exact
    nonnegative differences including zero. Resets, process changes, stale or missing samples and uncertain windows omit
    delta; missing values are never zero. Other outcomes can remain usable independently.

    """

    outcome: ObsRuntimeHTTPCronDeliveryName
    """Closed producer outcome. success is a settled successful attempt; retry is a retry disposition without a
    final result; app_error is an application-error classification. failed and dead_letter remain distinct delivery-
    failure classifications. Scheduled fires and manual runs share this metric."""
    state: ObsRuntimeHTTPCronDeliverySignalState
    reason: ObsRuntimeAppTaskOperationReason | Unset = UNSET
    """Closed source or comparison diagnostic. Previous-source reasons refer to the pinned earlier read. Changed
    process, repeated samples, resets and clock mismatches establish no delta."""
    current: ObsRuntimeAppTaskOperationPoint | Unset = UNSET
    """One finite exact nonnegative counter sample with its original scrape timestamp. A cumulative current value
    alone is not recent activity. The integer ceiling is wire safety, not an admission quota."""
    previous: ObsRuntimeAppTaskOperationPoint | Unset = UNSET
    """One finite exact nonnegative counter sample with its original scrape timestamp. A cumulative current value
    alone is not recent activity. The integer ceiling is wire safety, not an admission quota."""
    delta: int | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        outcome: str = self.outcome

        state: str = self.state

        reason: str | Unset = UNSET
        if not isinstance(self.reason, Unset):
            reason = self.reason

        current: dict[str, Any] | Unset = UNSET
        if not isinstance(self.current, Unset):
            current = self.current.to_dict()

        previous: dict[str, Any] | Unset = UNSET
        if not isinstance(self.previous, Unset):
            previous = self.previous.to_dict()

        delta = self.delta

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "outcome": outcome,
                "state": state,
            }
        )
        if reason is not UNSET:
            field_dict["reason"] = reason
        if current is not UNSET:
            field_dict["current"] = current
        if previous is not UNSET:
            field_dict["previous"] = previous
        if delta is not UNSET:
            field_dict["delta"] = delta

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.obs_runtime_app_task_operation_point import ObsRuntimeAppTaskOperationPoint

        d = dict(src_dict)
        outcome = check_obs_runtime_http_cron_delivery_name(d.pop("outcome"))

        state = check_obs_runtime_http_cron_delivery_signal_state(d.pop("state"))

        _reason = d.pop("reason", UNSET)
        reason: ObsRuntimeAppTaskOperationReason | Unset
        if isinstance(_reason, Unset):
            reason = UNSET
        else:
            reason = check_obs_runtime_app_task_operation_reason(_reason)

        _current = d.pop("current", UNSET)
        current: ObsRuntimeAppTaskOperationPoint | Unset
        if isinstance(_current, Unset):
            current = UNSET
        else:
            current = ObsRuntimeAppTaskOperationPoint.from_dict(_current)

        _previous = d.pop("previous", UNSET)
        previous: ObsRuntimeAppTaskOperationPoint | Unset
        if isinstance(_previous, Unset):
            previous = UNSET
        else:
            previous = ObsRuntimeAppTaskOperationPoint.from_dict(_previous)

        delta = d.pop("delta", UNSET)

        obs_runtime_http_cron_delivery_signal = cls(
            outcome=outcome,
            state=state,
            reason=reason,
            current=current,
            previous=previous,
            delta=delta,
        )

        obs_runtime_http_cron_delivery_signal.additional_properties = d
        return obs_runtime_http_cron_delivery_signal

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
