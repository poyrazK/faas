from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.runtime_policy_scheduler_status_scope import (
    RuntimePolicySchedulerStatusScope,
    check_runtime_policy_scheduler_status_scope,
)
from ..models.runtime_policy_scheduler_status_state import (
    RuntimePolicySchedulerStatusState,
    check_runtime_policy_scheduler_status_state,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="RuntimePolicySchedulerStatus")


@_attrs_define
class RuntimePolicySchedulerStatus:
    """Fresh observation of the desired scaling policy by the owning schedd. Active means the policy was loaded, not that a
    metric-driven replica target has been reached.

    """

    scope: RuntimePolicySchedulerStatusScope
    desired_revision: int
    observed_revision: int
    state: RuntimePolicySchedulerStatusState
    stale: bool
    scheduler_node_id: str | Unset = UNSET
    """Scheduler owner node when this app is sharded."""
    observed_at: datetime.datetime | None | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        scope: str = self.scope

        desired_revision = self.desired_revision

        observed_revision = self.observed_revision

        state: str = self.state

        stale = self.stale

        scheduler_node_id = self.scheduler_node_id

        observed_at: None | str | Unset
        if isinstance(self.observed_at, Unset):
            observed_at = UNSET
        elif isinstance(self.observed_at, datetime.datetime):
            observed_at = self.observed_at.isoformat()
        else:
            observed_at = self.observed_at

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "scope": scope,
                "desired_revision": desired_revision,
                "observed_revision": observed_revision,
                "state": state,
                "stale": stale,
            }
        )
        if scheduler_node_id is not UNSET:
            field_dict["scheduler_node_id"] = scheduler_node_id
        if observed_at is not UNSET:
            field_dict["observed_at"] = observed_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        scope = check_runtime_policy_scheduler_status_scope(d.pop("scope"))

        desired_revision = d.pop("desired_revision")

        observed_revision = d.pop("observed_revision")

        state = check_runtime_policy_scheduler_status_state(d.pop("state"))

        stale = d.pop("stale")

        scheduler_node_id = d.pop("scheduler_node_id", UNSET)

        def _parse_observed_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                observed_at_type_0 = datetime.datetime.fromisoformat(data)

                return observed_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        observed_at = _parse_observed_at(d.pop("observed_at", UNSET))

        runtime_policy_scheduler_status = cls(
            scope=scope,
            desired_revision=desired_revision,
            observed_revision=observed_revision,
            state=state,
            stale=stale,
            scheduler_node_id=scheduler_node_id,
            observed_at=observed_at,
        )

        runtime_policy_scheduler_status.additional_properties = d
        return runtime_policy_scheduler_status

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
