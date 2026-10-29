from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, Literal, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_input_health_available_roles_item import (
    ObsInputHealthAvailableRolesItem,
    check_obs_input_health_available_roles_item,
)
from ..models.obs_input_health_expected_role import ObsInputHealthExpectedRole, check_obs_input_health_expected_role
from ..models.obs_input_health_limitations_item import (
    ObsInputHealthLimitationsItem,
    check_obs_input_health_limitations_item,
)
from ..models.obs_input_health_reason import ObsInputHealthReason, check_obs_input_health_reason
from ..models.obs_input_health_state import ObsInputHealthState, check_obs_input_health_state
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.obs_input_job import ObsInputJob


T = TypeVar("T", bound="ObsInputHealth")


@_attrs_define
class ObsInputHealth:
    """Critical telemetry sample evidence for a selected minimum deployment role expectation."""

    generated_at: datetime.datetime
    scope: Literal["selected_critical_telemetry_inputs"]
    expected_role: ObsInputHealthExpectedRole
    available_roles: list[ObsInputHealthAvailableRolesItem]
    state: ObsInputHealthState
    max_age_seconds: Literal[120]
    lookback_seconds: Literal[600]
    jobs: list[ObsInputJob]
    """Fixed selected job inventory; empty before role selection, with full aggregate counts and no target preview."""
    limitations: list[ObsInputHealthLimitationsItem]
    reason: ObsInputHealthReason | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        generated_at = self.generated_at.isoformat()

        scope = self.scope

        expected_role: str = self.expected_role

        available_roles = []
        for available_roles_item_data in self.available_roles:
            available_roles_item: str = available_roles_item_data
            available_roles.append(available_roles_item)

        state: str = self.state

        max_age_seconds = self.max_age_seconds

        lookback_seconds = self.lookback_seconds

        jobs = []
        for jobs_item_data in self.jobs:
            jobs_item = jobs_item_data.to_dict()
            jobs.append(jobs_item)

        limitations = []
        for limitations_item_data in self.limitations:
            limitations_item: str = limitations_item_data
            limitations.append(limitations_item)

        reason: str | Unset = UNSET
        if not isinstance(self.reason, Unset):
            reason = self.reason

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "generated_at": generated_at,
                "scope": scope,
                "expected_role": expected_role,
                "available_roles": available_roles,
                "state": state,
                "max_age_seconds": max_age_seconds,
                "lookback_seconds": lookback_seconds,
                "jobs": jobs,
                "limitations": limitations,
            }
        )
        if reason is not UNSET:
            field_dict["reason"] = reason

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.obs_input_job import ObsInputJob

        d = dict(src_dict)
        generated_at = datetime.datetime.fromisoformat(d.pop("generated_at"))

        scope = cast(Literal["selected_critical_telemetry_inputs"], d.pop("scope"))
        if scope != "selected_critical_telemetry_inputs":
            raise ValueError(f"scope must match const 'selected_critical_telemetry_inputs', got '{scope}'")

        expected_role = check_obs_input_health_expected_role(d.pop("expected_role"))

        available_roles = []
        _available_roles = d.pop("available_roles")
        for available_roles_item_data in _available_roles:
            available_roles_item = check_obs_input_health_available_roles_item(available_roles_item_data)

            available_roles.append(available_roles_item)

        state = check_obs_input_health_state(d.pop("state"))

        max_age_seconds = cast(Literal[120], d.pop("max_age_seconds"))
        if max_age_seconds != 120:
            raise ValueError(f"max_age_seconds must match const 120, got '{max_age_seconds}'")

        lookback_seconds = cast(Literal[600], d.pop("lookback_seconds"))
        if lookback_seconds != 600:
            raise ValueError(f"lookback_seconds must match const 600, got '{lookback_seconds}'")

        jobs = []
        _jobs = d.pop("jobs")
        for jobs_item_data in _jobs:
            jobs_item = ObsInputJob.from_dict(jobs_item_data)

            jobs.append(jobs_item)

        limitations = []
        _limitations = d.pop("limitations")
        for limitations_item_data in _limitations:
            limitations_item = check_obs_input_health_limitations_item(limitations_item_data)

            limitations.append(limitations_item)

        _reason = d.pop("reason", UNSET)
        reason: ObsInputHealthReason | Unset
        if isinstance(_reason, Unset):
            reason = UNSET
        else:
            reason = check_obs_input_health_reason(_reason)

        obs_input_health = cls(
            generated_at=generated_at,
            scope=scope,
            expected_role=expected_role,
            available_roles=available_roles,
            state=state,
            max_age_seconds=max_age_seconds,
            lookback_seconds=lookback_seconds,
            jobs=jobs,
            limitations=limitations,
            reason=reason,
        )

        obs_input_health.additional_properties = d
        return obs_input_health

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
