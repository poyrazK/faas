from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_runtime_app_task_class_name import ObsRuntimeAppTaskClassName, check_obs_runtime_app_task_class_name
from ..models.obs_runtime_app_task_class_state import (
    ObsRuntimeAppTaskClassState,
    check_obs_runtime_app_task_class_state,
)
from ..models.obs_runtime_app_task_kind_name import ObsRuntimeAppTaskKindName, check_obs_runtime_app_task_kind_name
from ..models.obs_runtime_handoff_evidence_reason import (
    ObsRuntimeHandoffEvidenceReason,
    check_obs_runtime_handoff_evidence_reason,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.obs_runtime_app_task_class_values import ObsRuntimeAppTaskClassValues


T = TypeVar("T", bound="ObsRuntimeAppTaskClass")


@_attrs_define
class ObsRuntimeAppTaskClass:
    """One ordered kind/class observation. Values require successful fresh collection after the evidence boundary and
    coherent original sample clocks. Valid partial positives survive sibling gaps.

    """

    kind: ObsRuntimeAppTaskKindName
    """Closed durable task kind, separating release commands, manual tasks and cron tasks."""
    class_: ObsRuntimeAppTaskClassName
    """Closed durable task class. Restoring, retry_wait, held and unknown require all ages unknown. Running
    duration and held evidence do not establish an outage or bypass admission."""
    state: ObsRuntimeAppTaskClassState
    reason: ObsRuntimeHandoffEvidenceReason | Unset = UNSET
    """Closed reason for incomplete or unusable handoff evidence; collection prerequisites apply to backlog
    observations."""
    observed_at: datetime.datetime | Unset = UNSET
    values: ObsRuntimeAppTaskClassValues | Unset = UNSET
    """Durable task counts in one shared fleet view. Unknown age is included in pending. Known timing exists only
    when pending exceeds unknown_age. Restoring, retry_wait, held and unknown have only unknown ages. Known
    boundaries are queued admission timestamps, running start, cancellation request, lease expiry or missed start
    deadline, not continuous class residence or a new health threshold. Empty or entirely unknown timing omits both
    age fields. Integer ceiling is wire safety, not an admission quota."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        kind: str = self.kind

        class_: str = self.class_

        state: str = self.state

        reason: str | Unset = UNSET
        if not isinstance(self.reason, Unset):
            reason = self.reason

        observed_at: str | Unset = UNSET
        if not isinstance(self.observed_at, Unset):
            observed_at = self.observed_at.isoformat()

        values: dict[str, Any] | Unset = UNSET
        if not isinstance(self.values, Unset):
            values = self.values.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "kind": kind,
                "class": class_,
                "state": state,
            }
        )
        if reason is not UNSET:
            field_dict["reason"] = reason
        if observed_at is not UNSET:
            field_dict["observed_at"] = observed_at
        if values is not UNSET:
            field_dict["values"] = values

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.obs_runtime_app_task_class_values import ObsRuntimeAppTaskClassValues

        d = dict(src_dict)
        kind = check_obs_runtime_app_task_kind_name(d.pop("kind"))

        class_ = check_obs_runtime_app_task_class_name(d.pop("class"))

        state = check_obs_runtime_app_task_class_state(d.pop("state"))

        _reason = d.pop("reason", UNSET)
        reason: ObsRuntimeHandoffEvidenceReason | Unset
        if isinstance(_reason, Unset):
            reason = UNSET
        else:
            reason = check_obs_runtime_handoff_evidence_reason(_reason)

        _observed_at = d.pop("observed_at", UNSET)
        observed_at: datetime.datetime | Unset
        if isinstance(_observed_at, Unset):
            observed_at = UNSET
        else:
            observed_at = datetime.datetime.fromisoformat(_observed_at)

        _values = d.pop("values", UNSET)
        values: ObsRuntimeAppTaskClassValues | Unset
        if isinstance(_values, Unset):
            values = UNSET
        else:
            values = ObsRuntimeAppTaskClassValues.from_dict(_values)

        obs_runtime_app_task_class = cls(
            kind=kind,
            class_=class_,
            state=state,
            reason=reason,
            observed_at=observed_at,
            values=values,
        )

        obs_runtime_app_task_class.additional_properties = d
        return obs_runtime_app_task_class

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
