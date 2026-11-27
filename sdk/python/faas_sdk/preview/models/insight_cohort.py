from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.insight_cohort_request_kind import InsightCohortRequestKind, check_insight_cohort_request_kind
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.insight_cohort_request_where import InsightCohortRequestWhere
    from ..models.insight_step_request import InsightStepRequest


T = TypeVar("T", bound="InsightCohort")


@_attrs_define
class InsightCohort:
    """A saved cohort and its last refresh."""

    key: str
    """Cohort key, used in gregale.cohort rule conditions."""
    kind: InsightCohortRequestKind
    within_days: int
    """Look-back in days, at most the plan's analytics retention."""
    members: int
    """Customers in the cohort after the last refresh."""
    truncated: bool
    """More customers matched than max_members; the lowest customer IDs were kept."""
    max_members: int
    """The plan's member bound."""
    refresh_requested_at: datetime.datetime
    created_by: str
    created_at: datetime.datetime
    event: str | Unset = UNSET
    """The event for did and did_not."""
    where: InsightCohortRequestWhere | Unset = UNSET
    """Property values the did or did_not event must carry."""
    min_count: int | Unset = UNSET
    """did: minimum events (default 1)."""
    steps: list[InsightStepRequest] | Unset = UNSET
    """The funnel's steps."""
    window: str | Unset = UNSET
    """funnel: Go duration from the first step to the last, 1h to 720h."""
    refreshed_at: datetime.datetime | Unset = UNSET
    refresh_error: str | Unset = UNSET
    """Why the last refresh failed; the previous members are kept."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        key = self.key

        kind: str = self.kind

        within_days = self.within_days

        members = self.members

        truncated = self.truncated

        max_members = self.max_members

        refresh_requested_at = self.refresh_requested_at.isoformat()

        created_by = self.created_by

        created_at = self.created_at.isoformat()

        event = self.event

        where: dict[str, Any] | Unset = UNSET
        if not isinstance(self.where, Unset):
            where = self.where.to_dict()

        min_count = self.min_count

        steps: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.steps, Unset):
            steps = []
            for steps_item_data in self.steps:
                steps_item = steps_item_data.to_dict()
                steps.append(steps_item)

        window = self.window

        refreshed_at: str | Unset = UNSET
        if not isinstance(self.refreshed_at, Unset):
            refreshed_at = self.refreshed_at.isoformat()

        refresh_error = self.refresh_error

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "key": key,
                "kind": kind,
                "within_days": within_days,
                "members": members,
                "truncated": truncated,
                "max_members": max_members,
                "refresh_requested_at": refresh_requested_at,
                "created_by": created_by,
                "created_at": created_at,
            }
        )
        if event is not UNSET:
            field_dict["event"] = event
        if where is not UNSET:
            field_dict["where"] = where
        if min_count is not UNSET:
            field_dict["min_count"] = min_count
        if steps is not UNSET:
            field_dict["steps"] = steps
        if window is not UNSET:
            field_dict["window"] = window
        if refreshed_at is not UNSET:
            field_dict["refreshed_at"] = refreshed_at
        if refresh_error is not UNSET:
            field_dict["refresh_error"] = refresh_error

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.insight_cohort_request_where import InsightCohortRequestWhere
        from ..models.insight_step_request import InsightStepRequest

        d = dict(src_dict)
        key = d.pop("key")

        kind = check_insight_cohort_request_kind(d.pop("kind"))

        within_days = d.pop("within_days")

        members = d.pop("members")

        truncated = d.pop("truncated")

        max_members = d.pop("max_members")

        refresh_requested_at = datetime.datetime.fromisoformat(d.pop("refresh_requested_at"))

        created_by = d.pop("created_by")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        event = d.pop("event", UNSET)

        _where = d.pop("where", UNSET)
        where: InsightCohortRequestWhere | Unset
        if isinstance(_where, Unset):
            where = UNSET
        else:
            where = InsightCohortRequestWhere.from_dict(_where)

        min_count = d.pop("min_count", UNSET)

        _steps = d.pop("steps", UNSET)
        steps: list[InsightStepRequest] | Unset = UNSET
        if _steps is not UNSET:
            steps = []
            for steps_item_data in _steps:
                steps_item = InsightStepRequest.from_dict(steps_item_data)

                steps.append(steps_item)

        window = d.pop("window", UNSET)

        _refreshed_at = d.pop("refreshed_at", UNSET)
        refreshed_at: datetime.datetime | Unset
        if isinstance(_refreshed_at, Unset):
            refreshed_at = UNSET
        else:
            refreshed_at = datetime.datetime.fromisoformat(_refreshed_at)

        refresh_error = d.pop("refresh_error", UNSET)

        insight_cohort = cls(
            key=key,
            kind=kind,
            within_days=within_days,
            members=members,
            truncated=truncated,
            max_members=max_members,
            refresh_requested_at=refresh_requested_at,
            created_by=created_by,
            created_at=created_at,
            event=event,
            where=where,
            min_count=min_count,
            steps=steps,
            window=window,
            refreshed_at=refreshed_at,
            refresh_error=refresh_error,
        )

        insight_cohort.additional_properties = d
        return insight_cohort

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
