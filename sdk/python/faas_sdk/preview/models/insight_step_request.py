from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.insight_step_request_where import InsightStepRequestWhere


T = TypeVar("T", bound="InsightStepRequest")


@_attrs_define
class InsightStepRequest:
    """A funnel or retention step: an event and property values its events must carry."""

    event: str
    where: InsightStepRequestWhere | Unset = UNSET
    """Property values the step's events must carry."""

    def to_dict(self) -> dict[str, Any]:
        event = self.event

        where: dict[str, Any] | Unset = UNSET
        if not isinstance(self.where, Unset):
            where = self.where.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "event": event,
            }
        )
        if where is not UNSET:
            field_dict["where"] = where

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.insight_step_request_where import InsightStepRequestWhere

        d = dict(src_dict)
        event = d.pop("event")

        _where = d.pop("where", UNSET)
        where: InsightStepRequestWhere | Unset
        if isinstance(_where, Unset):
            where = UNSET
        else:
            where = InsightStepRequestWhere.from_dict(_where)

        insight_step_request = cls(
            event=event,
            where=where,
        )

        return insight_step_request
