from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.domain_doctor_check import DomainDoctorCheck


T = TypeVar("T", bound="DomainDoctorReport")


@_attrs_define
class DomainDoctorReport:
    """Per-domain doctor report (ADR-120). Carries 5 Render-style checks (dns_record, points_to_gregale, tls_certificate,
    caa_permits, ipv6_conflict) plus the durable row's app_id and observed_at. `stale:true` means the cached observation
    row was older than FAAS_DOMAIN_DOCTOR_TTL_SECONDS (default 300) when the handler ran a synchronous re-probe.

    """

    domain: str
    app_id: str
    observed_at: datetime.datetime
    healthy: bool
    checks: list[DomainDoctorCheck]
    stale: bool | Unset = False
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        domain = self.domain

        app_id = self.app_id

        observed_at = self.observed_at.isoformat()

        healthy = self.healthy

        checks = []
        for checks_item_data in self.checks:
            checks_item = checks_item_data.to_dict()
            checks.append(checks_item)

        stale = self.stale

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "domain": domain,
                "app_id": app_id,
                "observed_at": observed_at,
                "healthy": healthy,
                "checks": checks,
            }
        )
        if stale is not UNSET:
            field_dict["stale"] = stale

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.domain_doctor_check import DomainDoctorCheck

        d = dict(src_dict)
        domain = d.pop("domain")

        app_id = d.pop("app_id")

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        healthy = d.pop("healthy")

        checks = []
        _checks = d.pop("checks")
        for checks_item_data in _checks:
            checks_item = DomainDoctorCheck.from_dict(checks_item_data)

            checks.append(checks_item)

        stale = d.pop("stale", UNSET)

        domain_doctor_report = cls(
            domain=domain,
            app_id=app_id,
            observed_at=observed_at,
            healthy=healthy,
            checks=checks,
            stale=stale,
        )

        domain_doctor_report.additional_properties = d
        return domain_doctor_report

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
