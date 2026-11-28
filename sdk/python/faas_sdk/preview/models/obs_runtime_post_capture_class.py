from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.obs_runtime_handoff_evidence_reason import (
    ObsRuntimeHandoffEvidenceReason,
    check_obs_runtime_handoff_evidence_reason,
)
from ..models.obs_runtime_post_capture_class_name import (
    ObsRuntimePostCaptureClassName,
    check_obs_runtime_post_capture_class_name,
)
from ..models.obs_runtime_post_capture_class_state import (
    ObsRuntimePostCaptureClassState,
    check_obs_runtime_post_capture_class_state,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.obs_runtime_post_capture_class_values import ObsRuntimePostCaptureClassValues


T = TypeVar("T", bound="ObsRuntimePostCaptureClass")


@_attrs_define
class ObsRuntimePostCaptureClass:
    """One ordered post-capture evidence class. Values require fresh successful collection after the evidence boundary,
    fresh scrape evidence and original class clocks matching collection metadata. Valid positive evidence survives gaps
    in other classes.

    """

    class_: ObsRuntimePostCaptureClassName
    """Closed durable post-capture evidence class. Holds and overdue gates describe retained dependency,
    compatibility or verification evidence. They do not independently determine present deployment eligibility.
    Unknown class counts always have unknown ages."""
    state: ObsRuntimePostCaptureClassState
    reason: ObsRuntimeHandoffEvidenceReason | Unset = UNSET
    """Closed reason for incomplete or unusable handoff evidence; collection prerequisites apply to backlog
    observations."""
    observed_at: datetime.datetime | Unset = UNSET
    values: ObsRuntimePostCaptureClassValues | Unset = UNSET
    """Durable snapshot-flow evidence in this replica view. Unknown age is included in pending. Known boundary
    timing is required only when pending exceeds unknown_age; otherwise both timing fields are absent. A boundary
    can be capture park time, init publication or retained gate/verification evidence and may precede the rollout.
    Age is not continuous residence in a class, an execution deadline or proof of deployment readiness. The unknown
    class requires pending equal to unknown_age. The integer ceiling is a wire bound, not an admission quota."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
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
        from ..models.obs_runtime_post_capture_class_values import ObsRuntimePostCaptureClassValues

        d = dict(src_dict)
        class_ = check_obs_runtime_post_capture_class_name(d.pop("class"))

        state = check_obs_runtime_post_capture_class_state(d.pop("state"))

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
        values: ObsRuntimePostCaptureClassValues | Unset
        if isinstance(_values, Unset):
            values = UNSET
        else:
            values = ObsRuntimePostCaptureClassValues.from_dict(_values)

        obs_runtime_post_capture_class = cls(
            class_=class_,
            state=state,
            reason=reason,
            observed_at=observed_at,
            values=values,
        )

        obs_runtime_post_capture_class.additional_properties = d
        return obs_runtime_post_capture_class

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
