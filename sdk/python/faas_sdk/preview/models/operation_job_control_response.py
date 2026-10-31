from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="OperationJobControlResponse")


@_attrs_define
class OperationJobControlResponse:
    """Scoped observation of customer ownership, cancellation and current native task timing."""

    account_id: UUID
    app_id: UUID
    platform_tenant_id: UUID
    scope: str
    operation_id: UUID
    job_run_id: UUID
    generation: int
    attempt: int
    cancellation_requested: bool
    deadline_at: datetime.datetime
    lease_expires_at: datetime.datetime
    observed_at: datetime.datetime
    poll_after_ms: int

    def to_dict(self) -> dict[str, Any]:
        account_id = str(self.account_id)

        app_id = str(self.app_id)

        platform_tenant_id = str(self.platform_tenant_id)

        scope = self.scope

        operation_id = str(self.operation_id)

        job_run_id = str(self.job_run_id)

        generation = self.generation

        attempt = self.attempt

        cancellation_requested = self.cancellation_requested

        deadline_at = self.deadline_at.isoformat()

        lease_expires_at = self.lease_expires_at.isoformat()

        observed_at = self.observed_at.isoformat()

        poll_after_ms = self.poll_after_ms

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "account_id": account_id,
                "app_id": app_id,
                "platform_tenant_id": platform_tenant_id,
                "scope": scope,
                "operation_id": operation_id,
                "job_run_id": job_run_id,
                "generation": generation,
                "attempt": attempt,
                "cancellation_requested": cancellation_requested,
                "deadline_at": deadline_at,
                "lease_expires_at": lease_expires_at,
                "observed_at": observed_at,
                "poll_after_ms": poll_after_ms,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        account_id = UUID(d.pop("account_id"))

        app_id = UUID(d.pop("app_id"))

        platform_tenant_id = UUID(d.pop("platform_tenant_id"))

        scope = d.pop("scope")

        operation_id = UUID(d.pop("operation_id"))

        job_run_id = UUID(d.pop("job_run_id"))

        generation = d.pop("generation")

        attempt = d.pop("attempt")

        cancellation_requested = d.pop("cancellation_requested")

        deadline_at = datetime.datetime.fromisoformat(d.pop("deadline_at"))

        lease_expires_at = datetime.datetime.fromisoformat(d.pop("lease_expires_at"))

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        poll_after_ms = d.pop("poll_after_ms")

        operation_job_control_response = cls(
            account_id=account_id,
            app_id=app_id,
            platform_tenant_id=platform_tenant_id,
            scope=scope,
            operation_id=operation_id,
            job_run_id=job_run_id,
            generation=generation,
            attempt=attempt,
            cancellation_requested=cancellation_requested,
            deadline_at=deadline_at,
            lease_expires_at=lease_expires_at,
            observed_at=observed_at,
            poll_after_ms=poll_after_ms,
        )

        return operation_job_control_response
