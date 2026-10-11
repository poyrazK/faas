from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.operation_doctor_check_impact import OperationDoctorCheckImpact, check_operation_doctor_check_impact
from ..models.operation_doctor_check_status import OperationDoctorCheckStatus, check_operation_doctor_check_status
from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationDoctorCheck")


@_attrs_define
class OperationDoctorCheck:
    """One sanitized prerequisite, delivery configuration or unverified qualification observation."""

    check: str
    status: OperationDoctorCheckStatus
    impact: OperationDoctorCheckImpact
    code: str
    """Stable non-secret reason code; filesystem paths and infrastructure errors are not exposed."""
    message: str
    remediation: str | Unset = UNSET
    definition_id: UUID | Unset = UNSET
    name: str | Unset = UNSET
    revision: str | Unset = UNSET
    release_id: UUID | Unset = UNSET
    limit: int | Unset = UNSET
    observed: int | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        check = self.check

        status: str = self.status

        impact: str = self.impact

        code = self.code

        message = self.message

        remediation = self.remediation

        definition_id: str | Unset = UNSET
        if not isinstance(self.definition_id, Unset):
            definition_id = str(self.definition_id)

        name = self.name

        revision = self.revision

        release_id: str | Unset = UNSET
        if not isinstance(self.release_id, Unset):
            release_id = str(self.release_id)

        limit = self.limit

        observed = self.observed

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "check": check,
                "status": status,
                "impact": impact,
                "code": code,
                "message": message,
            }
        )
        if remediation is not UNSET:
            field_dict["remediation"] = remediation
        if definition_id is not UNSET:
            field_dict["definition_id"] = definition_id
        if name is not UNSET:
            field_dict["name"] = name
        if revision is not UNSET:
            field_dict["revision"] = revision
        if release_id is not UNSET:
            field_dict["release_id"] = release_id
        if limit is not UNSET:
            field_dict["limit"] = limit
        if observed is not UNSET:
            field_dict["observed"] = observed

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        check = d.pop("check")

        status = check_operation_doctor_check_status(d.pop("status"))

        impact = check_operation_doctor_check_impact(d.pop("impact"))

        code = d.pop("code")

        message = d.pop("message")

        remediation = d.pop("remediation", UNSET)

        _definition_id = d.pop("definition_id", UNSET)
        definition_id: UUID | Unset
        if isinstance(_definition_id, Unset):
            definition_id = UNSET
        else:
            definition_id = UUID(_definition_id)

        name = d.pop("name", UNSET)

        revision = d.pop("revision", UNSET)

        _release_id = d.pop("release_id", UNSET)
        release_id: UUID | Unset
        if isinstance(_release_id, Unset):
            release_id = UNSET
        else:
            release_id = UUID(_release_id)

        limit = d.pop("limit", UNSET)

        observed = d.pop("observed", UNSET)

        operation_doctor_check = cls(
            check=check,
            status=status,
            impact=impact,
            code=code,
            message=message,
            remediation=remediation,
            definition_id=definition_id,
            name=name,
            revision=revision,
            release_id=release_id,
            limit=limit,
            observed=observed,
        )

        return operation_doctor_check
