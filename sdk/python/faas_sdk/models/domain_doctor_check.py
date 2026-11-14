from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.domain_doctor_check_name import DomainDoctorCheckName, check_domain_doctor_check_name
from ..models.domain_doctor_check_status import DomainDoctorCheckStatus, check_domain_doctor_check_status
from ..types import UNSET, Unset

T = TypeVar("T", bound="DomainDoctorCheck")


@_attrs_define
class DomainDoctorCheck:
    """One row of the doctor report. Stable name tokens (dns_record / points_to_gregale / tls_certificate / caa_permits /
    ipv6_conflict) so the CLI can filter by name without parsing the human Detail field. Remediation is the exact record
    to change when status is fail — the load-bearing field for the activation drop-off.

    """

    name: DomainDoctorCheckName
    status: DomainDoctorCheckStatus
    detail: str
    observed: str | Unset = UNSET
    remediation: str | Unset = UNSET
    checked_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        name: str = self.name

        status: str = self.status

        detail = self.detail

        observed = self.observed

        remediation = self.remediation

        checked_at: str | Unset = UNSET
        if not isinstance(self.checked_at, Unset):
            checked_at = self.checked_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "name": name,
                "status": status,
                "detail": detail,
            }
        )
        if observed is not UNSET:
            field_dict["observed"] = observed
        if remediation is not UNSET:
            field_dict["remediation"] = remediation
        if checked_at is not UNSET:
            field_dict["checked_at"] = checked_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        name = check_domain_doctor_check_name(d.pop("name"))

        status = check_domain_doctor_check_status(d.pop("status"))

        detail = d.pop("detail")

        observed = d.pop("observed", UNSET)

        remediation = d.pop("remediation", UNSET)

        _checked_at = d.pop("checked_at", UNSET)
        checked_at: datetime.datetime | Unset
        if isinstance(_checked_at, Unset):
            checked_at = UNSET
        else:
            checked_at = datetime.datetime.fromisoformat(_checked_at)

        domain_doctor_check = cls(
            name=name,
            status=status,
            detail=detail,
            observed=observed,
            remediation=remediation,
            checked_at=checked_at,
        )

        domain_doctor_check.additional_properties = d
        return domain_doctor_check

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
