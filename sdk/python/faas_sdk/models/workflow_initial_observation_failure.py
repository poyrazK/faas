from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.workflow_initial_observation_failure_code import (
    WorkflowInitialObservationFailureCode,
    check_workflow_initial_observation_failure_code,
)

T = TypeVar("T", bound="WorkflowInitialObservationFailure")


@_attrs_define
class WorkflowInitialObservationFailure:
    """Last deterministic manual observation failure. Retry uses bounded backoff; a successful observation clears this
    metadata. Codes never include database error messages.

    """

    code: WorkflowInitialObservationFailureCode
    attempts: int
    failed_at: datetime.datetime
    retry_at: datetime.datetime

    def to_dict(self) -> dict[str, Any]:
        code: str = self.code

        attempts = self.attempts

        failed_at = self.failed_at.isoformat()

        retry_at = self.retry_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "code": code,
                "attempts": attempts,
                "failed_at": failed_at,
                "retry_at": retry_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        code = check_workflow_initial_observation_failure_code(d.pop("code"))

        attempts = d.pop("attempts")

        failed_at = datetime.datetime.fromisoformat(d.pop("failed_at"))

        retry_at = datetime.datetime.fromisoformat(d.pop("retry_at"))

        workflow_initial_observation_failure = cls(
            code=code,
            attempts=attempts,
            failed_at=failed_at,
            retry_at=retry_at,
        )

        return workflow_initial_observation_failure
