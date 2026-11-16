from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_serving_probe_inventory_state import (
    ObsServingProbeInventoryState,
    check_obs_serving_probe_inventory_state,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ObsServingProbeInventory")


@_attrs_define
class ObsServingProbeInventory:
    """Configuration and freshness of independent serving-probe intent."""

    state: ObsServingProbeInventoryState
    configured: bool | None
    expected_pairs: int | None
    observed_at: datetime.datetime | None
    reason: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        state: str = self.state

        configured: bool | None
        configured = self.configured

        expected_pairs: int | None
        expected_pairs = self.expected_pairs

        observed_at: None | str
        if isinstance(self.observed_at, datetime.datetime):
            observed_at = self.observed_at.isoformat()
        else:
            observed_at = self.observed_at

        reason = self.reason

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "state": state,
                "configured": configured,
                "expected_pairs": expected_pairs,
                "observed_at": observed_at,
            }
        )
        if reason is not UNSET:
            field_dict["reason"] = reason

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        state = check_obs_serving_probe_inventory_state(d.pop("state"))

        def _parse_configured(data: object) -> bool | None:
            if data is None:
                return data
            return cast(bool | None, data)

        configured = _parse_configured(d.pop("configured"))

        def _parse_expected_pairs(data: object) -> int | None:
            if data is None:
                return data
            return cast(int | None, data)

        expected_pairs = _parse_expected_pairs(d.pop("expected_pairs"))

        def _parse_observed_at(data: object) -> datetime.datetime | None:
            if data is None:
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                observed_at_type_0 = datetime.datetime.fromisoformat(data)

                return observed_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None, data)

        observed_at = _parse_observed_at(d.pop("observed_at"))

        reason = d.pop("reason", UNSET)

        obs_serving_probe_inventory = cls(
            state=state,
            configured=configured,
            expected_pairs=expected_pairs,
            observed_at=observed_at,
            reason=reason,
        )

        obs_serving_probe_inventory.additional_properties = d
        return obs_serving_probe_inventory

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
