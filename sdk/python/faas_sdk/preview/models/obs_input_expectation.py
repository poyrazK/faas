from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_input_expectation_reason import ObsInputExpectationReason, check_obs_input_expectation_reason
from ..models.obs_input_expectation_source import ObsInputExpectationSource, check_obs_input_expectation_source
from ..models.obs_input_expectation_state import ObsInputExpectationState, check_obs_input_expectation_state
from ..types import UNSET, Unset

T = TypeVar("T", bound="ObsInputExpectation")


@_attrs_define
class ObsInputExpectation:
    """Explicit static minimum or validated fresh compute discovery count; unknown counts remain null."""

    source: ObsInputExpectationSource
    state: ObsInputExpectationState
    minimum_targets: int | None
    """Minimum expected returned scrape targets; null when fresh discovery cannot establish a count."""
    invalid_targets: int | None
    """Omitted invalid compute registry rows; null for static minimums or unavailable discovery."""
    observed_at: datetime.datetime | None
    """Oldest original discovery sample or successful refresh time; null without validated discovery."""
    reason: ObsInputExpectationReason | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        source: str = self.source

        state: str = self.state

        minimum_targets: int | None
        minimum_targets = self.minimum_targets

        invalid_targets: int | None
        invalid_targets = self.invalid_targets

        observed_at: None | str
        if isinstance(self.observed_at, datetime.datetime):
            observed_at = self.observed_at.isoformat()
        else:
            observed_at = self.observed_at

        reason: str | Unset = UNSET
        if not isinstance(self.reason, Unset):
            reason = self.reason

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "source": source,
                "state": state,
                "minimum_targets": minimum_targets,
                "invalid_targets": invalid_targets,
                "observed_at": observed_at,
            }
        )
        if reason is not UNSET:
            field_dict["reason"] = reason

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        source = check_obs_input_expectation_source(d.pop("source"))

        state = check_obs_input_expectation_state(d.pop("state"))

        def _parse_minimum_targets(data: object) -> int | None:
            if data is None:
                return data
            return cast(int | None, data)

        minimum_targets = _parse_minimum_targets(d.pop("minimum_targets"))

        def _parse_invalid_targets(data: object) -> int | None:
            if data is None:
                return data
            return cast(int | None, data)

        invalid_targets = _parse_invalid_targets(d.pop("invalid_targets"))

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

        _reason = d.pop("reason", UNSET)
        reason: ObsInputExpectationReason | Unset
        if isinstance(_reason, Unset):
            reason = UNSET
        else:
            reason = check_obs_input_expectation_reason(_reason)

        obs_input_expectation = cls(
            source=source,
            state=state,
            minimum_targets=minimum_targets,
            invalid_targets=invalid_targets,
            observed_at=observed_at,
            reason=reason,
        )

        obs_input_expectation.additional_properties = d
        return obs_input_expectation

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
