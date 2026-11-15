from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_serving_probe_notification_channel import (
    ObsServingProbeNotificationChannel,
    check_obs_serving_probe_notification_channel,
)
from ..models.obs_serving_probe_notification_evidence_reason import (
    ObsServingProbeNotificationEvidenceReason,
    check_obs_serving_probe_notification_evidence_reason,
)
from ..models.obs_serving_probe_notification_failure_code import (
    ObsServingProbeNotificationFailureCode,
    check_obs_serving_probe_notification_failure_code,
)
from ..models.obs_serving_probe_notification_reason import (
    ObsServingProbeNotificationReason,
    check_obs_serving_probe_notification_reason,
)
from ..models.obs_serving_probe_notification_state import (
    ObsServingProbeNotificationState,
    check_obs_serving_probe_notification_state,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ObsServingProbeNotification")


@_attrs_define
class ObsServingProbeNotification:
    """Direct submission history for one node/channel. Clocks and outcome
    fields remain null or absent when unavailable. Evaluated_at describes
    the recording evaluation; last_run_at is the source probe-completion
    clock. Last_attempt_at and last_success_at are historical completed
    submission clocks, not provider delivery or acknowledgement times.

    """

    node: str
    channel: ObsServingProbeNotificationChannel
    state: ObsServingProbeNotificationState
    enabled: bool | None
    evaluated_at: datetime.datetime | None
    last_run_at: datetime.datetime | None
    last_attempt_at: datetime.datetime | None
    last_success_at: datetime.datetime | None
    reason: ObsServingProbeNotificationReason | Unset = UNSET
    evidence_reason: ObsServingProbeNotificationEvidenceReason | Unset = UNSET
    """Optional detail for a recording_unknown outcome from a coherent current evidence-status recording.
    Distinguishes valid enabled channels without history from missing or invalid telemetry and an unavailable
    source. Older rules omit this field. A current evaluation does not establish a submission or external receipt.
   """
    failure_code: ObsServingProbeNotificationFailureCode | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        node = self.node

        channel: str = self.channel

        state: str = self.state

        enabled: bool | None
        enabled = self.enabled

        evaluated_at: None | str
        if isinstance(self.evaluated_at, datetime.datetime):
            evaluated_at = self.evaluated_at.isoformat()
        else:
            evaluated_at = self.evaluated_at

        last_run_at: None | str
        if isinstance(self.last_run_at, datetime.datetime):
            last_run_at = self.last_run_at.isoformat()
        else:
            last_run_at = self.last_run_at

        last_attempt_at: None | str
        if isinstance(self.last_attempt_at, datetime.datetime):
            last_attempt_at = self.last_attempt_at.isoformat()
        else:
            last_attempt_at = self.last_attempt_at

        last_success_at: None | str
        if isinstance(self.last_success_at, datetime.datetime):
            last_success_at = self.last_success_at.isoformat()
        else:
            last_success_at = self.last_success_at

        reason: str | Unset = UNSET
        if not isinstance(self.reason, Unset):
            reason = self.reason

        evidence_reason: str | Unset = UNSET
        if not isinstance(self.evidence_reason, Unset):
            evidence_reason = self.evidence_reason

        failure_code: str | Unset = UNSET
        if not isinstance(self.failure_code, Unset):
            failure_code = self.failure_code

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "node": node,
                "channel": channel,
                "state": state,
                "enabled": enabled,
                "evaluated_at": evaluated_at,
                "last_run_at": last_run_at,
                "last_attempt_at": last_attempt_at,
                "last_success_at": last_success_at,
            }
        )
        if reason is not UNSET:
            field_dict["reason"] = reason
        if evidence_reason is not UNSET:
            field_dict["evidence_reason"] = evidence_reason
        if failure_code is not UNSET:
            field_dict["failure_code"] = failure_code

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        node = d.pop("node")

        channel = check_obs_serving_probe_notification_channel(d.pop("channel"))

        state = check_obs_serving_probe_notification_state(d.pop("state"))

        def _parse_enabled(data: object) -> bool | None:
            if data is None:
                return data
            return cast(bool | None, data)

        enabled = _parse_enabled(d.pop("enabled"))

        def _parse_evaluated_at(data: object) -> datetime.datetime | None:
            if data is None:
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                evaluated_at_type_0 = datetime.datetime.fromisoformat(data)

                return evaluated_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None, data)

        evaluated_at = _parse_evaluated_at(d.pop("evaluated_at"))

        def _parse_last_run_at(data: object) -> datetime.datetime | None:
            if data is None:
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                last_run_at_type_0 = datetime.datetime.fromisoformat(data)

                return last_run_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None, data)

        last_run_at = _parse_last_run_at(d.pop("last_run_at"))

        def _parse_last_attempt_at(data: object) -> datetime.datetime | None:
            if data is None:
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                last_attempt_at_type_0 = datetime.datetime.fromisoformat(data)

                return last_attempt_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None, data)

        last_attempt_at = _parse_last_attempt_at(d.pop("last_attempt_at"))

        def _parse_last_success_at(data: object) -> datetime.datetime | None:
            if data is None:
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                last_success_at_type_0 = datetime.datetime.fromisoformat(data)

                return last_success_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None, data)

        last_success_at = _parse_last_success_at(d.pop("last_success_at"))

        _reason = d.pop("reason", UNSET)
        reason: ObsServingProbeNotificationReason | Unset
        if isinstance(_reason, Unset):
            reason = UNSET
        else:
            reason = check_obs_serving_probe_notification_reason(_reason)

        _evidence_reason = d.pop("evidence_reason", UNSET)
        evidence_reason: ObsServingProbeNotificationEvidenceReason | Unset
        if isinstance(_evidence_reason, Unset):
            evidence_reason = UNSET
        else:
            evidence_reason = check_obs_serving_probe_notification_evidence_reason(_evidence_reason)

        _failure_code = d.pop("failure_code", UNSET)
        failure_code: ObsServingProbeNotificationFailureCode | Unset
        if isinstance(_failure_code, Unset):
            failure_code = UNSET
        else:
            failure_code = check_obs_serving_probe_notification_failure_code(_failure_code)

        obs_serving_probe_notification = cls(
            node=node,
            channel=channel,
            state=state,
            enabled=enabled,
            evaluated_at=evaluated_at,
            last_run_at=last_run_at,
            last_attempt_at=last_attempt_at,
            last_success_at=last_success_at,
            reason=reason,
            evidence_reason=evidence_reason,
            failure_code=failure_code,
        )

        obs_serving_probe_notification.additional_properties = d
        return obs_serving_probe_notification

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
