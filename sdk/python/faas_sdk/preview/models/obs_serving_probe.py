from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_serving_probe_state import ObsServingProbeState, check_obs_serving_probe_state
from ..types import UNSET, Unset

T = TypeVar("T", bound="ObsServingProbe")


@_attrs_define
class ObsServingProbe:
    """One expected node/URL pair. Raw URL bytes remain private; url_id fingerprints exact URL bytes, including query
    parameters. Evaluation time describes the recording, and last_run_at is the probe loop clock.

    """

    node: str
    url_id: str
    state: ObsServingProbeState
    reporting: bool | None
    evaluated_at: datetime.datetime | None
    last_run_at: datetime.datetime | None
    expected_first_byte_seconds: float
    max_result_age_seconds: float
    reason: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        node = self.node

        url_id = self.url_id

        state: str = self.state

        reporting: bool | None
        reporting = self.reporting

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

        expected_first_byte_seconds = self.expected_first_byte_seconds

        max_result_age_seconds = self.max_result_age_seconds

        reason = self.reason

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "node": node,
                "url_id": url_id,
                "state": state,
                "reporting": reporting,
                "evaluated_at": evaluated_at,
                "last_run_at": last_run_at,
                "expected_first_byte_seconds": expected_first_byte_seconds,
                "max_result_age_seconds": max_result_age_seconds,
            }
        )
        if reason is not UNSET:
            field_dict["reason"] = reason

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        node = d.pop("node")

        url_id = d.pop("url_id")

        state = check_obs_serving_probe_state(d.pop("state"))

        def _parse_reporting(data: object) -> bool | None:
            if data is None:
                return data
            return cast(bool | None, data)

        reporting = _parse_reporting(d.pop("reporting"))

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

        expected_first_byte_seconds = d.pop("expected_first_byte_seconds")

        max_result_age_seconds = d.pop("max_result_age_seconds")

        reason = d.pop("reason", UNSET)

        obs_serving_probe = cls(
            node=node,
            url_id=url_id,
            state=state,
            reporting=reporting,
            evaluated_at=evaluated_at,
            last_run_at=last_run_at,
            expected_first_byte_seconds=expected_first_byte_seconds,
            max_result_age_seconds=max_result_age_seconds,
            reason=reason,
        )

        obs_serving_probe.additional_properties = d
        return obs_serving_probe

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
