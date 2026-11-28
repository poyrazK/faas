from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.insight_cohort_request_kind import InsightCohortRequestKind, check_insight_cohort_request_kind
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.insight_cohort_request_where import InsightCohortRequestWhere
    from ..models.insight_step_request import InsightStepRequest


T = TypeVar("T", bound="InsightCohortRequest")


@_attrs_define
class InsightCohortRequest:
    """A cohort to save. did: customers that sent the event at least min_count times in the last within_days days; did_not:
    customers active in the last 30 days that did not send it; funnel: customers that completed the steps within the
    window, starting in the last within_days days.

    """

    key: str
    """Cohort key, used in gregale.cohort rule conditions."""
    kind: InsightCohortRequestKind
    within_days: int
    """Look-back in days, at most the plan's analytics retention."""
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

    def to_dict(self) -> dict[str, Any]:
        key = self.key

        kind: str = self.kind

        within_days = self.within_days

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

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "key": key,
                "kind": kind,
                "within_days": within_days,
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

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.insight_cohort_request_where import InsightCohortRequestWhere
        from ..models.insight_step_request import InsightStepRequest

        d = dict(src_dict)
        key = d.pop("key")

        kind = check_insight_cohort_request_kind(d.pop("kind"))

        within_days = d.pop("within_days")

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

        insight_cohort_request = cls(
            key=key,
            kind=kind,
            within_days=within_days,
            event=event,
            where=where,
            min_count=min_count,
            steps=steps,
            window=window,
        )

        return insight_cohort_request
