from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.flag_experiment_retention_period import (
    FlagExperimentRetentionPeriod,
    check_flag_experiment_retention_period,
)

if TYPE_CHECKING:
    from ..models.insight_step_request import InsightStepRequest


T = TypeVar("T", bound="FlagExperimentRetention")


@_attrs_define
class FlagExperimentRetention:
    """A retention metric: a customer converts when it sends the return event in period N after its first exposure."""

    return_: InsightStepRequest
    """A funnel or retention step: an event and property values its events must carry."""
    period: FlagExperimentRetentionPeriod
    period_number: int
    """The period after exposure that counts as returning."""

    def to_dict(self) -> dict[str, Any]:
        return_ = self.return_.to_dict()

        period: str = self.period

        period_number = self.period_number

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "return": return_,
                "period": period,
                "period_number": period_number,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.insight_step_request import InsightStepRequest

        d = dict(src_dict)
        return_ = InsightStepRequest.from_dict(d.pop("return"))

        period = check_flag_experiment_retention_period(d.pop("period"))

        period_number = d.pop("period_number")

        flag_experiment_retention = cls(
            return_=return_,
            period=period,
            period_number=period_number,
        )

        return flag_experiment_retention
