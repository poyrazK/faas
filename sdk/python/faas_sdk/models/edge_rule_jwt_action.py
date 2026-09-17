from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.edge_rule_jwt_action_algorithms_item import (
    EdgeRuleJWTActionAlgorithmsItem,
    check_edge_rule_jwt_action_algorithms_item,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.edge_rule_jwt_action_required_claims import EdgeRuleJWTActionRequiredClaims


T = TypeVar("T", bound="EdgeRuleJWTAction")


@_attrs_define
class EdgeRuleJWTAction:
    """Validates an inbound Bearer JWT against a JWKS endpoint. The
    algorithm allowlist is asymmetric-only and the selected JWK's
    `alg` metadata must match the JWT header; a JWK with `use=enc`
    is rejected (RFC 8725 §3.1, RFC 7517).

    """

    issuer: str
    jwks_url: str
    algorithms: list[EdgeRuleJWTActionAlgorithmsItem]
    audience: list[str] | Unset = UNSET
    required_claims: EdgeRuleJWTActionRequiredClaims | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        issuer = self.issuer

        jwks_url = self.jwks_url

        algorithms = []
        for algorithms_item_data in self.algorithms:
            algorithms_item: str = algorithms_item_data
            algorithms.append(algorithms_item)

        audience: list[str] | Unset = UNSET
        if not isinstance(self.audience, Unset):
            audience = self.audience

        required_claims: dict[str, Any] | Unset = UNSET
        if not isinstance(self.required_claims, Unset):
            required_claims = self.required_claims.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "issuer": issuer,
                "jwks_url": jwks_url,
                "algorithms": algorithms,
            }
        )
        if audience is not UNSET:
            field_dict["audience"] = audience
        if required_claims is not UNSET:
            field_dict["required_claims"] = required_claims

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.edge_rule_jwt_action_required_claims import EdgeRuleJWTActionRequiredClaims

        d = dict(src_dict)
        issuer = d.pop("issuer")

        jwks_url = d.pop("jwks_url")

        algorithms = []
        _algorithms = d.pop("algorithms")
        for algorithms_item_data in _algorithms:
            algorithms_item = check_edge_rule_jwt_action_algorithms_item(algorithms_item_data)

            algorithms.append(algorithms_item)

        audience = cast(list[str], d.pop("audience", UNSET))

        _required_claims = d.pop("required_claims", UNSET)
        required_claims: EdgeRuleJWTActionRequiredClaims | Unset
        if isinstance(_required_claims, Unset):
            required_claims = UNSET
        else:
            required_claims = EdgeRuleJWTActionRequiredClaims.from_dict(_required_claims)

        edge_rule_jwt_action = cls(
            issuer=issuer,
            jwks_url=jwks_url,
            algorithms=algorithms,
            audience=audience,
            required_claims=required_claims,
        )

        edge_rule_jwt_action.additional_properties = d
        return edge_rule_jwt_action

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
