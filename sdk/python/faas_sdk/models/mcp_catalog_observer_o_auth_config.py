from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.mcp_catalog_observer_o_auth_config_profile import (
    MCPCatalogObserverOAuthConfigProfile,
    check_mcp_catalog_observer_o_auth_config_profile,
)
from ..models.mcp_catalog_observer_o_auth_config_revocation_auth_method import (
    MCPCatalogObserverOAuthConfigRevocationAuthMethod,
    check_mcp_catalog_observer_o_auth_config_revocation_auth_method,
)
from ..models.mcp_catalog_observer_o_auth_config_token_auth_method import (
    MCPCatalogObserverOAuthConfigTokenAuthMethod,
    check_mcp_catalog_observer_o_auth_config_token_auth_method,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="MCPCatalogObserverOAuthConfig")


@_attrs_define
class MCPCatalogObserverOAuthConfig:
    """Explicit OAuth refresh grant for one app and role; credentials are app secret references in the connection
    environment.

    """

    token_endpoint: str
    """Explicit public HTTPS token endpoint without query or fragment; redirects and private egress are refused"""
    client_id: str
    revocation_endpoint: str | Unset = UNSET
    """Explicit HTTPS RFC 7009 revocation endpoint; omission requires manual provider revocation"""
    revocation_auth_method: MCPCatalogObserverOAuthConfigRevocationAuthMethod | Unset = UNSET
    """Defaults to token authentication except Google none"""
    refresh_token_secret_ref: str | Unset = UNSET
    """App secret containing the initial refresh token; rotation resets the connection"""
    client_secret_ref: str | Unset = UNSET
    """Optional app secret for OAuth client_secret_basic authentication"""
    guided: bool | Unset = UNSET
    """Use browser authorization; mutually exclusive with an initial refresh secret"""
    authorization_endpoint: str | Unset = UNSET
    """Explicit HTTPS browser authorization endpoint required for guided connections"""
    scopes: list[str] | Unset = UNSET
    profile: MCPCatalogObserverOAuthConfigProfile | Unset = UNSET
    """Explicit provider behavior; Google requests offline consent and OIDC adds openid and offline_access"""
    token_auth_method: MCPCatalogObserverOAuthConfigTokenAuthMethod | Unset = UNSET
    """Explicit token authentication; defaults to public none or confidential basic except Google confidential post"""
    browser_redirect_uri: str | Unset = UNSET
    """Registered HTTPS console callback for guided browser authorization; omitted for loopback flows"""
    callback_port: int | Unset = UNSET
    """Guided loopback callback port; 0 chooses a dynamic port"""
    resource: str | Unset = UNSET
    """Reviewed HTTPS MCP resource indicator sent unchanged in authorization"""
    issuer: str | Unset = UNSET
    """Reviewed provider issuer paired with discovery_url"""
    discovery_url: str | Unset = UNSET
    """Explicit guarded metadata URL; endpoint mismatches and declared incompatibilities block new authorization"""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        token_endpoint = self.token_endpoint

        client_id = self.client_id

        revocation_endpoint = self.revocation_endpoint

        revocation_auth_method: str | Unset = UNSET
        if not isinstance(self.revocation_auth_method, Unset):
            revocation_auth_method = self.revocation_auth_method

        refresh_token_secret_ref = self.refresh_token_secret_ref

        client_secret_ref = self.client_secret_ref

        guided = self.guided

        authorization_endpoint = self.authorization_endpoint

        scopes: list[str] | Unset = UNSET
        if not isinstance(self.scopes, Unset):
            scopes = self.scopes

        profile: str | Unset = UNSET
        if not isinstance(self.profile, Unset):
            profile = self.profile

        token_auth_method: str | Unset = UNSET
        if not isinstance(self.token_auth_method, Unset):
            token_auth_method = self.token_auth_method

        browser_redirect_uri = self.browser_redirect_uri

        callback_port = self.callback_port

        resource = self.resource

        issuer = self.issuer

        discovery_url = self.discovery_url

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "token_endpoint": token_endpoint,
                "client_id": client_id,
            }
        )
        if revocation_endpoint is not UNSET:
            field_dict["revocation_endpoint"] = revocation_endpoint
        if revocation_auth_method is not UNSET:
            field_dict["revocation_auth_method"] = revocation_auth_method
        if refresh_token_secret_ref is not UNSET:
            field_dict["refresh_token_secret_ref"] = refresh_token_secret_ref
        if client_secret_ref is not UNSET:
            field_dict["client_secret_ref"] = client_secret_ref
        if guided is not UNSET:
            field_dict["guided"] = guided
        if authorization_endpoint is not UNSET:
            field_dict["authorization_endpoint"] = authorization_endpoint
        if scopes is not UNSET:
            field_dict["scopes"] = scopes
        if profile is not UNSET:
            field_dict["profile"] = profile
        if token_auth_method is not UNSET:
            field_dict["token_auth_method"] = token_auth_method
        if browser_redirect_uri is not UNSET:
            field_dict["browser_redirect_uri"] = browser_redirect_uri
        if callback_port is not UNSET:
            field_dict["callback_port"] = callback_port
        if resource is not UNSET:
            field_dict["resource"] = resource
        if issuer is not UNSET:
            field_dict["issuer"] = issuer
        if discovery_url is not UNSET:
            field_dict["discovery_url"] = discovery_url

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        token_endpoint = d.pop("token_endpoint")

        client_id = d.pop("client_id")

        revocation_endpoint = d.pop("revocation_endpoint", UNSET)

        _revocation_auth_method = d.pop("revocation_auth_method", UNSET)
        revocation_auth_method: MCPCatalogObserverOAuthConfigRevocationAuthMethod | Unset
        if isinstance(_revocation_auth_method, Unset):
            revocation_auth_method = UNSET
        else:
            revocation_auth_method = check_mcp_catalog_observer_o_auth_config_revocation_auth_method(
                _revocation_auth_method
            )

        refresh_token_secret_ref = d.pop("refresh_token_secret_ref", UNSET)

        client_secret_ref = d.pop("client_secret_ref", UNSET)

        guided = d.pop("guided", UNSET)

        authorization_endpoint = d.pop("authorization_endpoint", UNSET)

        scopes = cast(list[str], d.pop("scopes", UNSET))

        _profile = d.pop("profile", UNSET)
        profile: MCPCatalogObserverOAuthConfigProfile | Unset
        if isinstance(_profile, Unset):
            profile = UNSET
        else:
            profile = check_mcp_catalog_observer_o_auth_config_profile(_profile)

        _token_auth_method = d.pop("token_auth_method", UNSET)
        token_auth_method: MCPCatalogObserverOAuthConfigTokenAuthMethod | Unset
        if isinstance(_token_auth_method, Unset):
            token_auth_method = UNSET
        else:
            token_auth_method = check_mcp_catalog_observer_o_auth_config_token_auth_method(_token_auth_method)

        browser_redirect_uri = d.pop("browser_redirect_uri", UNSET)

        callback_port = d.pop("callback_port", UNSET)

        resource = d.pop("resource", UNSET)

        issuer = d.pop("issuer", UNSET)

        discovery_url = d.pop("discovery_url", UNSET)

        mcp_catalog_observer_o_auth_config = cls(
            token_endpoint=token_endpoint,
            client_id=client_id,
            revocation_endpoint=revocation_endpoint,
            revocation_auth_method=revocation_auth_method,
            refresh_token_secret_ref=refresh_token_secret_ref,
            client_secret_ref=client_secret_ref,
            guided=guided,
            authorization_endpoint=authorization_endpoint,
            scopes=scopes,
            profile=profile,
            token_auth_method=token_auth_method,
            browser_redirect_uri=browser_redirect_uri,
            callback_port=callback_port,
            resource=resource,
            issuer=issuer,
            discovery_url=discovery_url,
        )

        mcp_catalog_observer_o_auth_config.additional_properties = d
        return mcp_catalog_observer_o_auth_config

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
