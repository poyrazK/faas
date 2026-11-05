from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.o_auth_token_exchange_request_grant_type import (
    OAuthTokenExchangeRequestGrantType,
    check_o_auth_token_exchange_request_grant_type,
)
from ..models.o_auth_token_exchange_request_requested_token_type import (
    OAuthTokenExchangeRequestRequestedTokenType,
    check_o_auth_token_exchange_request_requested_token_type,
)
from ..models.o_auth_token_exchange_request_scope import (
    OAuthTokenExchangeRequestScope,
    check_o_auth_token_exchange_request_scope,
)
from ..models.o_auth_token_exchange_request_subject_token_type import (
    OAuthTokenExchangeRequestSubjectTokenType,
    check_o_auth_token_exchange_request_subject_token_type,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="OAuthTokenExchangeRequest")


@_attrs_define
class OAuthTokenExchangeRequest:
    """RFC 8693 form-encoded request profile for the OIDC exchange endpoint.
    Gregale requires one `audience` because it selects the account trust
    policy, accepts JWT subject tokens only, and issues only deploy:write
    access tokens. `resource` and actor-token delegation are unsupported.

    """

    grant_type: OAuthTokenExchangeRequestGrantType
    subject_token: str
    """IdP-issued JWT to exchange for a short-lived deploy bearer."""
    subject_token_type: OAuthTokenExchangeRequestSubjectTokenType
    audience: str
    """Trust-policy audience pinned in the subject JWT."""
    requested_token_type: OAuthTokenExchangeRequestRequestedTokenType | Unset = UNSET
    scope: OAuthTokenExchangeRequestScope | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        grant_type: str = self.grant_type

        subject_token = self.subject_token

        subject_token_type: str = self.subject_token_type

        audience = self.audience

        requested_token_type: str | Unset = UNSET
        if not isinstance(self.requested_token_type, Unset):
            requested_token_type = self.requested_token_type

        scope: str | Unset = UNSET
        if not isinstance(self.scope, Unset):
            scope = self.scope

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "grant_type": grant_type,
                "subject_token": subject_token,
                "subject_token_type": subject_token_type,
                "audience": audience,
            }
        )
        if requested_token_type is not UNSET:
            field_dict["requested_token_type"] = requested_token_type
        if scope is not UNSET:
            field_dict["scope"] = scope

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        grant_type = check_o_auth_token_exchange_request_grant_type(d.pop("grant_type"))

        subject_token = d.pop("subject_token")

        subject_token_type = check_o_auth_token_exchange_request_subject_token_type(d.pop("subject_token_type"))

        audience = d.pop("audience")

        _requested_token_type = d.pop("requested_token_type", UNSET)
        requested_token_type: OAuthTokenExchangeRequestRequestedTokenType | Unset
        if isinstance(_requested_token_type, Unset):
            requested_token_type = UNSET
        else:
            requested_token_type = check_o_auth_token_exchange_request_requested_token_type(_requested_token_type)

        _scope = d.pop("scope", UNSET)
        scope: OAuthTokenExchangeRequestScope | Unset
        if isinstance(_scope, Unset):
            scope = UNSET
        else:
            scope = check_o_auth_token_exchange_request_scope(_scope)

        o_auth_token_exchange_request = cls(
            grant_type=grant_type,
            subject_token=subject_token,
            subject_token_type=subject_token_type,
            audience=audience,
            requested_token_type=requested_token_type,
            scope=scope,
        )

        o_auth_token_exchange_request.additional_properties = d
        return o_auth_token_exchange_request

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
