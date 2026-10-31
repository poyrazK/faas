from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.workflow_schedule_catch_up_preview_outcome import (
    WorkflowScheduleCatchUpPreviewOutcome,
    check_workflow_schedule_catch_up_preview_outcome,
)
from ..models.workflow_schedule_catch_up_preview_policy import (
    WorkflowScheduleCatchUpPreviewPolicy,
    check_workflow_schedule_catch_up_preview_policy,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.workflow_schedule_fire_preview import WorkflowScheduleFirePreview


T = TypeVar("T", bound="WorkflowScheduleCatchUpPreview")


@_attrs_define
class WorkflowScheduleCatchUpPreview:
    """Simulated next evaluator decision. It does not reserve quota or promise worker availability."""

    policy: WorkflowScheduleCatchUpPreviewPolicy
    outcome: WorkflowScheduleCatchUpPreviewOutcome
    eligible_occurrences: int
    """Candidate occurrences within the latest policy window, or the single current occurrence for skip."""
    coalesced_occurrences: int
    """Older eligible fires represented by the selected latest occurrence."""
    missed_occurrences_not_recovered: bool
    """True when the skip policy drops earlier fires before the current occurrence."""
    missed_outside_window: bool
    """True when at least one unconsumed fire is older than the latest policy window."""
    window: str | Unset = UNSET
    """Effective maximum age for latest catch-up."""
    since: datetime.datetime | Unset = UNSET
    """Durable or simulated prior evaluator time."""
    selected: WorkflowScheduleFirePreview | Unset = UNSET
    """One canonical fire time with its offset-bearing local representation."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        policy: str = self.policy

        outcome: str = self.outcome

        eligible_occurrences = self.eligible_occurrences

        coalesced_occurrences = self.coalesced_occurrences

        missed_occurrences_not_recovered = self.missed_occurrences_not_recovered

        missed_outside_window = self.missed_outside_window

        window = self.window

        since: str | Unset = UNSET
        if not isinstance(self.since, Unset):
            since = self.since.isoformat()

        selected: dict[str, Any] | Unset = UNSET
        if not isinstance(self.selected, Unset):
            selected = self.selected.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "policy": policy,
                "outcome": outcome,
                "eligible_occurrences": eligible_occurrences,
                "coalesced_occurrences": coalesced_occurrences,
                "missed_occurrences_not_recovered": missed_occurrences_not_recovered,
                "missed_outside_window": missed_outside_window,
            }
        )
        if window is not UNSET:
            field_dict["window"] = window
        if since is not UNSET:
            field_dict["since"] = since
        if selected is not UNSET:
            field_dict["selected"] = selected

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.workflow_schedule_fire_preview import WorkflowScheduleFirePreview

        d = dict(src_dict)
        policy = check_workflow_schedule_catch_up_preview_policy(d.pop("policy"))

        outcome = check_workflow_schedule_catch_up_preview_outcome(d.pop("outcome"))

        eligible_occurrences = d.pop("eligible_occurrences")

        coalesced_occurrences = d.pop("coalesced_occurrences")

        missed_occurrences_not_recovered = d.pop("missed_occurrences_not_recovered")

        missed_outside_window = d.pop("missed_outside_window")

        window = d.pop("window", UNSET)

        _since = d.pop("since", UNSET)
        since: datetime.datetime | Unset
        if isinstance(_since, Unset):
            since = UNSET
        else:
            since = datetime.datetime.fromisoformat(_since)

        _selected = d.pop("selected", UNSET)
        selected: WorkflowScheduleFirePreview | Unset
        if isinstance(_selected, Unset):
            selected = UNSET
        else:
            selected = WorkflowScheduleFirePreview.from_dict(_selected)

        workflow_schedule_catch_up_preview = cls(
            policy=policy,
            outcome=outcome,
            eligible_occurrences=eligible_occurrences,
            coalesced_occurrences=coalesced_occurrences,
            missed_occurrences_not_recovered=missed_occurrences_not_recovered,
            missed_outside_window=missed_outside_window,
            window=window,
            since=since,
            selected=selected,
        )

        workflow_schedule_catch_up_preview.additional_properties = d
        return workflow_schedule_catch_up_preview

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
