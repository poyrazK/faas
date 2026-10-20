from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.operation_doctor_response_observation_scope import (
    OperationDoctorResponseObservationScope,
    check_operation_doctor_response_observation_scope,
)
from ..models.operation_doctor_response_plan import OperationDoctorResponsePlan, check_operation_doctor_response_plan
from ..models.operation_doctor_response_submission_state import (
    OperationDoctorResponseSubmissionState,
    check_operation_doctor_response_submission_state,
)

if TYPE_CHECKING:
    from ..models.operation_doctor_check import OperationDoctorCheck


T = TypeVar("T", bound="OperationDoctorResponse")


@_attrs_define
class OperationDoctorResponse:
    """Read-only prerequisite observations for an owned deployment and tenant on one API node."""

    app_id: UUID
    scope: str
    deployment_id: UUID
    platform_tenant_id: UUID
    plan: OperationDoctorResponsePlan
    observed_at: datetime.datetime
    observation_scope: OperationDoctorResponseObservationScope
    submission_state: OperationDoctorResponseSubmissionState
    """Submission prerequisites only. Delivery warnings and unverified qualification do not determine this
    observation."""
    checks: list[OperationDoctorCheck]

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        scope = self.scope

        deployment_id = str(self.deployment_id)

        platform_tenant_id = str(self.platform_tenant_id)

        plan: str = self.plan

        observed_at = self.observed_at.isoformat()

        observation_scope: str = self.observation_scope

        submission_state: str = self.submission_state

        checks = []
        for checks_item_data in self.checks:
            checks_item = checks_item_data.to_dict()
            checks.append(checks_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "app_id": app_id,
                "scope": scope,
                "deployment_id": deployment_id,
                "platform_tenant_id": platform_tenant_id,
                "plan": plan,
                "observed_at": observed_at,
                "observation_scope": observation_scope,
                "submission_state": submission_state,
                "checks": checks,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_doctor_check import OperationDoctorCheck

        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        scope = d.pop("scope")

        deployment_id = UUID(d.pop("deployment_id"))

        platform_tenant_id = UUID(d.pop("platform_tenant_id"))

        plan = check_operation_doctor_response_plan(d.pop("plan"))

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        observation_scope = check_operation_doctor_response_observation_scope(d.pop("observation_scope"))

        submission_state = check_operation_doctor_response_submission_state(d.pop("submission_state"))

        checks = []
        _checks = d.pop("checks")
        for checks_item_data in _checks:
            checks_item = OperationDoctorCheck.from_dict(checks_item_data)

            checks.append(checks_item)

        operation_doctor_response = cls(
            app_id=app_id,
            scope=scope,
            deployment_id=deployment_id,
            platform_tenant_id=platform_tenant_id,
            plan=plan,
            observed_at=observed_at,
            observation_scope=observation_scope,
            submission_state=submission_state,
            checks=checks,
        )

        return operation_doctor_response
