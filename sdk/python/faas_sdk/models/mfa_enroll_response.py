from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="MFAEnrollResponse")


@_attrs_define
class MFAEnrollResponse:
    """One-shot enrollment payload. Returned exactly once on
    /enroll. The server persists `mfa_secret_encrypted` (sealed)
    and `mfa_recovery_codes_hash` (SHA-256d); subsequent calls
    to /enroll overwrite the secret + codes but do NOT
    re-surface the plaintexts to the dashboard.

    """

    otpauth_url: str
    """ Standard `otpauth://totp/...` URL with the issuer = FaaS.
    The customer's authenticator app ingests this on its own;
    the dashboard also embeds the QR for camera-based setup.
     """
    secret: str
    """ Base32-encoded TOTP secret, 32 chars (no padding). Same
    value embedded in the otpauth URL; surfaced here so the
    dashboard can render the secret directly for
    copy-paste into an authenticator app that doesn't read
    URLs.
     """
    qr_code_png_base64: str
    """ Base64-encoded PNG bytes of the QR code (256×256). The
    server base64-encodes the raw PNG for JSON transport;
    the dashboard decodes the string back to bytes before
    rendering it in an `<img>` tag. The authenticator scans
    the decoded PNG.
     """
    recovery_codes: list[str]
    """ Ten single-use 10-character base32 strings. The
    dashboard renders them in the "save these somewhere"
    step. Each code is hashed (SHA-256) before storage;
    the plaintext never reappears.
     """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        otpauth_url = self.otpauth_url

        secret = self.secret

        qr_code_png_base64 = self.qr_code_png_base64

        recovery_codes = self.recovery_codes

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "otpauth_url": otpauth_url,
                "secret": secret,
                "qr_code_png_base64": qr_code_png_base64,
                "recovery_codes": recovery_codes,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        otpauth_url = d.pop("otpauth_url")

        secret = d.pop("secret")

        qr_code_png_base64 = d.pop("qr_code_png_base64")

        recovery_codes = cast(list[str], d.pop("recovery_codes"))

        mfa_enroll_response = cls(
            otpauth_url=otpauth_url,
            secret=secret,
            qr_code_png_base64=qr_code_png_base64,
            recovery_codes=recovery_codes,
        )

        mfa_enroll_response.additional_properties = d
        return mfa_enroll_response

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
