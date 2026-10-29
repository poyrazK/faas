from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.operation_attempt_logs_correlation import (
    OperationAttemptLogsCorrelation,
    check_operation_attempt_logs_correlation,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationAttemptLogs")


@_attrs_define
class OperationAttemptLogs:
    """Instance log window of an HTTP attempt. Lines other requests wrote on the same instance during the window
    interleave.

    """

    instance_id: UUID
    since: datetime.datetime
    archive_dates: list[datetime.date]
    correlation: OperationAttemptLogsCorrelation
    until: datetime.datetime | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        instance_id = str(self.instance_id)

        since = self.since.isoformat()

        archive_dates = []
        for archive_dates_item_data in self.archive_dates:
            archive_dates_item = archive_dates_item_data.isoformat()
            archive_dates.append(archive_dates_item)

        correlation: str = self.correlation

        until: str | Unset = UNSET
        if not isinstance(self.until, Unset):
            until = self.until.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "instance_id": instance_id,
                "since": since,
                "archive_dates": archive_dates,
                "correlation": correlation,
            }
        )
        if until is not UNSET:
            field_dict["until"] = until

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        instance_id = UUID(d.pop("instance_id"))

        since = datetime.datetime.fromisoformat(d.pop("since"))

        archive_dates = []
        _archive_dates = d.pop("archive_dates")
        for archive_dates_item_data in _archive_dates:
            archive_dates_item = datetime.date.fromisoformat(archive_dates_item_data)

            archive_dates.append(archive_dates_item)

        correlation = check_operation_attempt_logs_correlation(d.pop("correlation"))

        _until = d.pop("until", UNSET)
        until: datetime.datetime | Unset
        if isinstance(_until, Unset):
            until = UNSET
        else:
            until = datetime.datetime.fromisoformat(_until)

        operation_attempt_logs = cls(
            instance_id=instance_id,
            since=since,
            archive_dates=archive_dates,
            correlation=correlation,
            until=until,
        )

        return operation_attempt_logs
