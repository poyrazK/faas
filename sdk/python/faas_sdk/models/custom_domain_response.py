from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.dns_record_instruction import DNSRecordInstruction
    from ..models.domain_doctor_check import DomainDoctorCheck
    from ..models.domain_readiness import DomainReadiness


T = TypeVar("T", bound="CustomDomainResponse")


@_attrs_define
class CustomDomainResponse:
    """A custom domain binding: domain string, target app, optional project environment, verification status, and TLS
    provisioning state. adds `default`, `cert_not_after`, and `cert_sans` for the `gregale domains set-default | verify
    | show` surface.

    """

    domain: str
    app_id: str
    verified: bool
    readiness: DomainReadiness | Unset = UNSET
    """Independent ownership, DNS routing, and TLS activation state."""
    verification_next_check_at: datetime.datetime | Unset = UNSET
    """Next scheduled automatic ownership check; omitted after verification."""
    verification_attempts: int | Unset = UNSET
    cert_retry_at: datetime.datetime | Unset = UNSET
    """Next permitted legacy certificate attempt, when a durable cooldown is active. Caddy manages its own retry
    schedule."""
    checks: list[DomainDoctorCheck] | Unset = UNSET
    """Fresh checks for this claim, including observed values and remediation. Stale observations are omitted."""
    environment: str | Unset = UNSET
    """Project environment that owns the route; omitted for legacy application-wide domains."""
    challenge_token: None | str | Unset = UNSET
    verified_at: datetime.datetime | None | Unset = UNSET
    verification_expires_at: datetime.datetime | Unset = UNSET
    """Hard deadline for an unverified claim. An expired claim requires a fresh create request and a new TXT proof."""
    verification_expired: bool | Unset = UNSET
    """True when the unverified claim has expired. Obsolete challenge_token, txt_record, and verification DNS
    instructions are omitted; resume domain setup to recreate the claim."""
    txt_record: None | str | Unset = UNSET
    default: bool | Unset = UNSET
    """True when this domain is the app's default. Set via `gregale domains set-default`."""
    cert_not_after: datetime.datetime | None | Unset = UNSET
    """Issued cert NotAfter (RFC3339 UTC). Populated on verified domains; the `gregale domains show` line below the
    cert expiry renders against this field."""
    cert_sans: list[str] | Unset = UNSET
    """Cert subject alt names (DNSNames). Useful for the `gregale domains show` listing — if the customer's CNAME
    points at a CDN, the SANs reveal which CDN."""
    cert_status: None | str | Unset = UNSET
    """Durable TLS lifecycle for the legacy custom domain (pending, issued, renewing, failed, or dns_drifted). The
    show endpoint may temporarily return a live `dial_failed:<reason>` value when the probe cannot reach the edge.
   """
    cert_expires_at: datetime.datetime | None | Unset = UNSET
    """Durable certificate expiry timestamp recorded by the cert observer."""
    cert_last_error: None | str | Unset = UNSET
    """Most recent certificate issuance/renewal error, when cert_status is failed."""
    dns_last_checked_at: datetime.datetime | None | Unset = UNSET
    """Timestamp of the most recent DNS verification/doctor probe."""
    dns_records: list[DNSRecordInstruction] | Unset = UNSET
    """DNS records to publish: the TXT ownership proof and the routing CNAME, plus A/AAAA alternatives for a zone
    apex when the platform publishes edge addresses."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        domain = self.domain

        app_id = self.app_id

        verified = self.verified

        readiness: dict[str, Any] | Unset = UNSET
        if not isinstance(self.readiness, Unset):
            readiness = self.readiness.to_dict()

        verification_next_check_at: str | Unset = UNSET
        if not isinstance(self.verification_next_check_at, Unset):
            verification_next_check_at = self.verification_next_check_at.isoformat()

        verification_attempts = self.verification_attempts

        cert_retry_at: str | Unset = UNSET
        if not isinstance(self.cert_retry_at, Unset):
            cert_retry_at = self.cert_retry_at.isoformat()

        checks: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.checks, Unset):
            checks = []
            for checks_item_data in self.checks:
                checks_item = checks_item_data.to_dict()
                checks.append(checks_item)

        environment = self.environment

        challenge_token: None | str | Unset
        if isinstance(self.challenge_token, Unset):
            challenge_token = UNSET
        else:
            challenge_token = self.challenge_token

        verified_at: None | str | Unset
        if isinstance(self.verified_at, Unset):
            verified_at = UNSET
        elif isinstance(self.verified_at, datetime.datetime):
            verified_at = self.verified_at.isoformat()
        else:
            verified_at = self.verified_at

        verification_expires_at: str | Unset = UNSET
        if not isinstance(self.verification_expires_at, Unset):
            verification_expires_at = self.verification_expires_at.isoformat()

        verification_expired = self.verification_expired

        txt_record: None | str | Unset
        if isinstance(self.txt_record, Unset):
            txt_record = UNSET
        else:
            txt_record = self.txt_record

        default = self.default

        cert_not_after: None | str | Unset
        if isinstance(self.cert_not_after, Unset):
            cert_not_after = UNSET
        elif isinstance(self.cert_not_after, datetime.datetime):
            cert_not_after = self.cert_not_after.isoformat()
        else:
            cert_not_after = self.cert_not_after

        cert_sans: list[str] | Unset = UNSET
        if not isinstance(self.cert_sans, Unset):
            cert_sans = self.cert_sans

        cert_status: None | str | Unset
        if isinstance(self.cert_status, Unset):
            cert_status = UNSET
        else:
            cert_status = self.cert_status

        cert_expires_at: None | str | Unset
        if isinstance(self.cert_expires_at, Unset):
            cert_expires_at = UNSET
        elif isinstance(self.cert_expires_at, datetime.datetime):
            cert_expires_at = self.cert_expires_at.isoformat()
        else:
            cert_expires_at = self.cert_expires_at

        cert_last_error: None | str | Unset
        if isinstance(self.cert_last_error, Unset):
            cert_last_error = UNSET
        else:
            cert_last_error = self.cert_last_error

        dns_last_checked_at: None | str | Unset
        if isinstance(self.dns_last_checked_at, Unset):
            dns_last_checked_at = UNSET
        elif isinstance(self.dns_last_checked_at, datetime.datetime):
            dns_last_checked_at = self.dns_last_checked_at.isoformat()
        else:
            dns_last_checked_at = self.dns_last_checked_at

        dns_records: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.dns_records, Unset):
            dns_records = []
            for dns_records_item_data in self.dns_records:
                dns_records_item = dns_records_item_data.to_dict()
                dns_records.append(dns_records_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "domain": domain,
                "app_id": app_id,
                "verified": verified,
            }
        )
        if readiness is not UNSET:
            field_dict["readiness"] = readiness
        if verification_next_check_at is not UNSET:
            field_dict["verification_next_check_at"] = verification_next_check_at
        if verification_attempts is not UNSET:
            field_dict["verification_attempts"] = verification_attempts
        if cert_retry_at is not UNSET:
            field_dict["cert_retry_at"] = cert_retry_at
        if checks is not UNSET:
            field_dict["checks"] = checks
        if environment is not UNSET:
            field_dict["environment"] = environment
        if challenge_token is not UNSET:
            field_dict["challenge_token"] = challenge_token
        if verified_at is not UNSET:
            field_dict["verified_at"] = verified_at
        if verification_expires_at is not UNSET:
            field_dict["verification_expires_at"] = verification_expires_at
        if verification_expired is not UNSET:
            field_dict["verification_expired"] = verification_expired
        if txt_record is not UNSET:
            field_dict["txt_record"] = txt_record
        if default is not UNSET:
            field_dict["default"] = default
        if cert_not_after is not UNSET:
            field_dict["cert_not_after"] = cert_not_after
        if cert_sans is not UNSET:
            field_dict["cert_sans"] = cert_sans
        if cert_status is not UNSET:
            field_dict["cert_status"] = cert_status
        if cert_expires_at is not UNSET:
            field_dict["cert_expires_at"] = cert_expires_at
        if cert_last_error is not UNSET:
            field_dict["cert_last_error"] = cert_last_error
        if dns_last_checked_at is not UNSET:
            field_dict["dns_last_checked_at"] = dns_last_checked_at
        if dns_records is not UNSET:
            field_dict["dns_records"] = dns_records

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.dns_record_instruction import DNSRecordInstruction
        from ..models.domain_doctor_check import DomainDoctorCheck
        from ..models.domain_readiness import DomainReadiness

        d = dict(src_dict)
        domain = d.pop("domain")

        app_id = d.pop("app_id")

        verified = d.pop("verified")

        _readiness = d.pop("readiness", UNSET)
        readiness: DomainReadiness | Unset
        if isinstance(_readiness, Unset):
            readiness = UNSET
        else:
            readiness = DomainReadiness.from_dict(_readiness)

        _verification_next_check_at = d.pop("verification_next_check_at", UNSET)
        verification_next_check_at: datetime.datetime | Unset
        if isinstance(_verification_next_check_at, Unset):
            verification_next_check_at = UNSET
        else:
            verification_next_check_at = datetime.datetime.fromisoformat(_verification_next_check_at)

        verification_attempts = d.pop("verification_attempts", UNSET)

        _cert_retry_at = d.pop("cert_retry_at", UNSET)
        cert_retry_at: datetime.datetime | Unset
        if isinstance(_cert_retry_at, Unset):
            cert_retry_at = UNSET
        else:
            cert_retry_at = datetime.datetime.fromisoformat(_cert_retry_at)

        _checks = d.pop("checks", UNSET)
        checks: list[DomainDoctorCheck] | Unset = UNSET
        if _checks is not UNSET:
            checks = []
            for checks_item_data in _checks:
                checks_item = DomainDoctorCheck.from_dict(checks_item_data)

                checks.append(checks_item)

        environment = d.pop("environment", UNSET)

        def _parse_challenge_token(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        challenge_token = _parse_challenge_token(d.pop("challenge_token", UNSET))

        def _parse_verified_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                verified_at_type_0 = datetime.datetime.fromisoformat(data)

                return verified_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        verified_at = _parse_verified_at(d.pop("verified_at", UNSET))

        _verification_expires_at = d.pop("verification_expires_at", UNSET)
        verification_expires_at: datetime.datetime | Unset
        if isinstance(_verification_expires_at, Unset):
            verification_expires_at = UNSET
        else:
            verification_expires_at = datetime.datetime.fromisoformat(_verification_expires_at)

        verification_expired = d.pop("verification_expired", UNSET)

        def _parse_txt_record(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        txt_record = _parse_txt_record(d.pop("txt_record", UNSET))

        default = d.pop("default", UNSET)

        def _parse_cert_not_after(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                cert_not_after_type_0 = datetime.datetime.fromisoformat(data)

                return cert_not_after_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        cert_not_after = _parse_cert_not_after(d.pop("cert_not_after", UNSET))

        cert_sans = cast(list[str], d.pop("cert_sans", UNSET))

        def _parse_cert_status(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        cert_status = _parse_cert_status(d.pop("cert_status", UNSET))

        def _parse_cert_expires_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                cert_expires_at_type_0 = datetime.datetime.fromisoformat(data)

                return cert_expires_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        cert_expires_at = _parse_cert_expires_at(d.pop("cert_expires_at", UNSET))

        def _parse_cert_last_error(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        cert_last_error = _parse_cert_last_error(d.pop("cert_last_error", UNSET))

        def _parse_dns_last_checked_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                dns_last_checked_at_type_0 = datetime.datetime.fromisoformat(data)

                return dns_last_checked_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        dns_last_checked_at = _parse_dns_last_checked_at(d.pop("dns_last_checked_at", UNSET))

        _dns_records = d.pop("dns_records", UNSET)
        dns_records: list[DNSRecordInstruction] | Unset = UNSET
        if _dns_records is not UNSET:
            dns_records = []
            for dns_records_item_data in _dns_records:
                dns_records_item = DNSRecordInstruction.from_dict(dns_records_item_data)

                dns_records.append(dns_records_item)

        custom_domain_response = cls(
            domain=domain,
            app_id=app_id,
            verified=verified,
            readiness=readiness,
            verification_next_check_at=verification_next_check_at,
            verification_attempts=verification_attempts,
            cert_retry_at=cert_retry_at,
            checks=checks,
            environment=environment,
            challenge_token=challenge_token,
            verified_at=verified_at,
            verification_expires_at=verification_expires_at,
            verification_expired=verification_expired,
            txt_record=txt_record,
            default=default,
            cert_not_after=cert_not_after,
            cert_sans=cert_sans,
            cert_status=cert_status,
            cert_expires_at=cert_expires_at,
            cert_last_error=cert_last_error,
            dns_last_checked_at=dns_last_checked_at,
            dns_records=dns_records,
        )

        custom_domain_response.additional_properties = d
        return custom_domain_response

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
