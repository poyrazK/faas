from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="CustomDomainResponse")


@_attrs_define
class CustomDomainResponse:
    """A custom domain binding: domain string, target app, verification status, and TLS provisioning state. Issue #961 /
    Mega-A PR-3 adds `default`, `cert_not_after`, and `cert_sans` for the `gregale domains set-default | verify | show`
    surface.

    """

    domain: str
    app_id: str
    verified: bool
    challenge_token: None | str | Unset = UNSET
    verified_at: datetime.datetime | None | Unset = UNSET
    txt_record: None | str | Unset = UNSET
    default: bool | Unset = UNSET
    """True when this domain is the app's default (issue #961 / Mega-A PR-3). Set via `gregale domains set-
    default`."""
    cert_not_after: datetime.datetime | None | Unset = UNSET
    """Issued cert NotAfter (RFC3339 UTC). Populated on verified domains; the `gregale domains show` line below the
    cert expiry renders against this field."""
    cert_sans: list[str] | Unset = UNSET
    """Cert subject alt names (DNSNames). Useful for the `gregale domains show` listing — if the customer's CNAME
    points at a CDN, the SANs reveal which CDN."""
    cert_status: None | str | Unset = UNSET
    """Durable TLS lifecycle for the legacy custom domain (pending, issued, renewing, or failed; issue #1397 / F1).
    The show endpoint may temporarily return a live `dial_failed:<reason>` value when the probe cannot reach the
    edge."""
    cert_expires_at: datetime.datetime | None | Unset = UNSET
    """Durable certificate expiry timestamp recorded by the cert observer."""
    cert_last_error: None | str | Unset = UNSET
    """Most recent certificate issuance/renewal error, when cert_status is failed."""
    dns_last_checked_at: datetime.datetime | None | Unset = UNSET
    """Timestamp of the most recent DNS verification/doctor probe."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        domain = self.domain

        app_id = self.app_id

        verified = self.verified

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

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "domain": domain,
                "app_id": app_id,
                "verified": verified,
            }
        )
        if challenge_token is not UNSET:
            field_dict["challenge_token"] = challenge_token
        if verified_at is not UNSET:
            field_dict["verified_at"] = verified_at
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

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        domain = d.pop("domain")

        app_id = d.pop("app_id")

        verified = d.pop("verified")

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

        custom_domain_response = cls(
            domain=domain,
            app_id=app_id,
            verified=verified,
            challenge_token=challenge_token,
            verified_at=verified_at,
            txt_record=txt_record,
            default=default,
            cert_not_after=cert_not_after,
            cert_sans=cert_sans,
            cert_status=cert_status,
            cert_expires_at=cert_expires_at,
            cert_last_error=cert_last_error,
            dns_last_checked_at=dns_last_checked_at,
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
