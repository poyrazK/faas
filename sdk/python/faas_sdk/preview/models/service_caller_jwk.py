from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.service_caller_jwk_alg import ServiceCallerJWKAlg, check_service_caller_jwk_alg
from ..models.service_caller_jwk_crv import ServiceCallerJWKCrv, check_service_caller_jwk_crv
from ..models.service_caller_jwk_kty import ServiceCallerJWKKty, check_service_caller_jwk_kty
from ..models.service_caller_jwk_use import ServiceCallerJWKUse, check_service_caller_jwk_use

T = TypeVar("T", bound="ServiceCallerJWK")


@_attrs_define
class ServiceCallerJWK:
    """Public Ed25519 key used to verify a signed service-caller assertion."""

    kty: ServiceCallerJWKKty
    crv: ServiceCallerJWKCrv
    kid: str
    x: str
    alg: ServiceCallerJWKAlg
    use: ServiceCallerJWKUse
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        kty: str = self.kty

        crv: str = self.crv

        kid = self.kid

        x = self.x

        alg: str = self.alg

        use: str = self.use

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "kty": kty,
                "crv": crv,
                "kid": kid,
                "x": x,
                "alg": alg,
                "use": use,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        kty = check_service_caller_jwk_kty(d.pop("kty"))

        crv = check_service_caller_jwk_crv(d.pop("crv"))

        kid = d.pop("kid")

        x = d.pop("x")

        alg = check_service_caller_jwk_alg(d.pop("alg"))

        use = check_service_caller_jwk_use(d.pop("use"))

        service_caller_jwk = cls(
            kty=kty,
            crv=crv,
            kid=kid,
            x=x,
            alg=alg,
            use=use,
        )

        service_caller_jwk.additional_properties = d
        return service_caller_jwk

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
