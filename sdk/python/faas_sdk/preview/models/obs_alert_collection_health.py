from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, Literal, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_alert_collection_health_collection_state import (
    ObsAlertCollectionHealthCollectionState,
    check_obs_alert_collection_health_collection_state,
)
from ..models.obs_alert_collection_health_state import (
    ObsAlertCollectionHealthState,
    check_obs_alert_collection_health_state,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ObsAlertCollectionHealth")


@_attrs_define
class ObsAlertCollectionHealth:
    """Durable collection receipt and explicit producer configuration or health state."""

    state: ObsAlertCollectionHealthState
    diagnostics_path: Literal["/v1/admin/obs/rule-health"] | Unset = UNSET
    """Known private diagnostics route advertised as an optional bundle feature marker; reads do not establish past
    failure causation."""
    collection_state: ObsAlertCollectionHealthCollectionState | Unset = UNSET
    reason: str | Unset = UNSET
    last_attempt_at: datetime.datetime | Unset = UNSET
    last_success_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        state: str = self.state

        diagnostics_path = self.diagnostics_path

        collection_state: str | Unset = UNSET
        if not isinstance(self.collection_state, Unset):
            collection_state = self.collection_state

        reason = self.reason

        last_attempt_at: str | Unset = UNSET
        if not isinstance(self.last_attempt_at, Unset):
            last_attempt_at = self.last_attempt_at.isoformat()

        last_success_at: str | Unset = UNSET
        if not isinstance(self.last_success_at, Unset):
            last_success_at = self.last_success_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "state": state,
            }
        )
        if diagnostics_path is not UNSET:
            field_dict["diagnostics_path"] = diagnostics_path
        if collection_state is not UNSET:
            field_dict["collection_state"] = collection_state
        if reason is not UNSET:
            field_dict["reason"] = reason
        if last_attempt_at is not UNSET:
            field_dict["last_attempt_at"] = last_attempt_at
        if last_success_at is not UNSET:
            field_dict["last_success_at"] = last_success_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        state = check_obs_alert_collection_health_state(d.pop("state"))

        diagnostics_path = cast(Literal["/v1/admin/obs/rule-health"] | Unset, d.pop("diagnostics_path", UNSET))
        if diagnostics_path != "/v1/admin/obs/rule-health" and not isinstance(diagnostics_path, Unset):
            raise ValueError(f"diagnostics_path must match const '/v1/admin/obs/rule-health', got '{diagnostics_path}'")

        _collection_state = d.pop("collection_state", UNSET)
        collection_state: ObsAlertCollectionHealthCollectionState | Unset
        if isinstance(_collection_state, Unset):
            collection_state = UNSET
        else:
            collection_state = check_obs_alert_collection_health_collection_state(_collection_state)

        reason = d.pop("reason", UNSET)

        _last_attempt_at = d.pop("last_attempt_at", UNSET)
        last_attempt_at: datetime.datetime | Unset
        if isinstance(_last_attempt_at, Unset):
            last_attempt_at = UNSET
        else:
            last_attempt_at = datetime.datetime.fromisoformat(_last_attempt_at)

        _last_success_at = d.pop("last_success_at", UNSET)
        last_success_at: datetime.datetime | Unset
        if isinstance(_last_success_at, Unset):
            last_success_at = UNSET
        else:
            last_success_at = datetime.datetime.fromisoformat(_last_success_at)

        obs_alert_collection_health = cls(
            state=state,
            diagnostics_path=diagnostics_path,
            collection_state=collection_state,
            reason=reason,
            last_attempt_at=last_attempt_at,
            last_success_at=last_success_at,
        )

        obs_alert_collection_health.additional_properties = d
        return obs_alert_collection_health

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
