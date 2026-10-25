from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.o_auth_token_exchange_request import OAuthTokenExchangeRequest
from ...models.o_auth_token_exchange_response import OAuthTokenExchangeResponse
from ...models.oidc_exchange_request import OIDCExchangeRequest
from ...models.oidc_exchange_response import OIDCExchangeResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    body: OIDCExchangeRequest | OAuthTokenExchangeRequest | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/auth/oidc/exchange",
    }

    if isinstance(body, OIDCExchangeRequest):
        _kwargs["json"] = body.to_dict()

        headers["Content-Type"] = "application/json"
    if isinstance(body, OAuthTokenExchangeRequest):
        _kwargs["data"] = body.to_dict()
        headers["Content-Type"] = "application/x-www-form-urlencoded"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> OAuthTokenExchangeResponse | OIDCExchangeResponse | Problem | None:
    if response.status_code == 200:

        def _parse_response_200(data: object) -> OAuthTokenExchangeResponse | OIDCExchangeResponse:
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                response_200_type_0 = OIDCExchangeResponse.from_dict(data)

                return response_200_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            if not isinstance(data, dict):
                raise TypeError()
            response_200_type_1 = OAuthTokenExchangeResponse.from_dict(data)

            return response_200_type_1

        response_200 = _parse_response_200(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[OAuthTokenExchangeResponse | OIDCExchangeResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: OIDCExchangeRequest | OAuthTokenExchangeRequest | Unset = UNSET,
) -> Response[OAuthTokenExchangeResponse | OIDCExchangeResponse | Problem]:
    """Exchange an IdP-issued JWT for a short-lived deploy bearer

     ADR-101 / issue #270. CI runners that have an IdP-issued OIDC
    JWT (e.g. GitHub Actions `ACTIONS_ID_TOKEN_REQUEST_TOKEN`,
    GitLab CI, CircleCI) call this endpoint to exchange it for a
    short-lived opaque bearer (5 min TTL, `fp_oidc_<48 hex>` prefix).
    The bearer is then used in `Authorization: Bearer …` on the
    existing deploy routes.

    Gregale publishes the supported OAuth capability metadata at
    `/.well-known/oauth-authorization-server` (RFC 8414). That document
    advertises this token-exchange grant and intentionally omits
    unsupported authorization-code, refresh-token, and client-registration
    capabilities.

    The endpoint is anonymous — the JWT is the auth — so it does
    not require a session or a previous bearer. The first-use
    auto-create flow bootstraps a permissive trust policy on the
    `(account_id, issuer_url)` pair so customers do not have to
    configure the dashboard before their first CI deploy.

    The AuthLimit surface is the shared per-IP bucket (spec §11
    10/min/IP) — high-volume CI runners may hit the cap; long-lived
    deploy tokens remain the escape hatch.

    The endpoint also accepts the RFC 8693 OAuth 2.0 Token Exchange
    profile as `application/x-www-form-urlencoded`. That profile uses
    `subject_token` (a JWT), `subject_token_type` set to the registered
    JWT identifier, and one `audience`; it returns an opaque deploy bearer
    as an OAuth `access_token`. The legacy JSON shape remains supported.

    Args:
        body (OIDCExchangeRequest): Body for `POST /v1/auth/oidc/exchange` (ADR-101).
        body (OAuthTokenExchangeRequest): RFC 8693 form-encoded request profile for the OIDC
            exchange endpoint.
            Gregale requires one `audience` because it selects the account trust
            policy, accepts JWT subject tokens only, and issues only deploy:write
            access tokens. `resource` and actor-token delegation are unsupported.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OAuthTokenExchangeResponse | OIDCExchangeResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    body: OIDCExchangeRequest | OAuthTokenExchangeRequest | Unset = UNSET,
) -> OAuthTokenExchangeResponse | OIDCExchangeResponse | Problem | None:
    """Exchange an IdP-issued JWT for a short-lived deploy bearer

     ADR-101 / issue #270. CI runners that have an IdP-issued OIDC
    JWT (e.g. GitHub Actions `ACTIONS_ID_TOKEN_REQUEST_TOKEN`,
    GitLab CI, CircleCI) call this endpoint to exchange it for a
    short-lived opaque bearer (5 min TTL, `fp_oidc_<48 hex>` prefix).
    The bearer is then used in `Authorization: Bearer …` on the
    existing deploy routes.

    Gregale publishes the supported OAuth capability metadata at
    `/.well-known/oauth-authorization-server` (RFC 8414). That document
    advertises this token-exchange grant and intentionally omits
    unsupported authorization-code, refresh-token, and client-registration
    capabilities.

    The endpoint is anonymous — the JWT is the auth — so it does
    not require a session or a previous bearer. The first-use
    auto-create flow bootstraps a permissive trust policy on the
    `(account_id, issuer_url)` pair so customers do not have to
    configure the dashboard before their first CI deploy.

    The AuthLimit surface is the shared per-IP bucket (spec §11
    10/min/IP) — high-volume CI runners may hit the cap; long-lived
    deploy tokens remain the escape hatch.

    The endpoint also accepts the RFC 8693 OAuth 2.0 Token Exchange
    profile as `application/x-www-form-urlencoded`. That profile uses
    `subject_token` (a JWT), `subject_token_type` set to the registered
    JWT identifier, and one `audience`; it returns an opaque deploy bearer
    as an OAuth `access_token`. The legacy JSON shape remains supported.

    Args:
        body (OIDCExchangeRequest): Body for `POST /v1/auth/oidc/exchange` (ADR-101).
        body (OAuthTokenExchangeRequest): RFC 8693 form-encoded request profile for the OIDC
            exchange endpoint.
            Gregale requires one `audience` because it selects the account trust
            policy, accepts JWT subject tokens only, and issues only deploy:write
            access tokens. `resource` and actor-token delegation are unsupported.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OAuthTokenExchangeResponse | OIDCExchangeResponse | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: OIDCExchangeRequest | OAuthTokenExchangeRequest | Unset = UNSET,
) -> Response[OAuthTokenExchangeResponse | OIDCExchangeResponse | Problem]:
    """Exchange an IdP-issued JWT for a short-lived deploy bearer

     ADR-101 / issue #270. CI runners that have an IdP-issued OIDC
    JWT (e.g. GitHub Actions `ACTIONS_ID_TOKEN_REQUEST_TOKEN`,
    GitLab CI, CircleCI) call this endpoint to exchange it for a
    short-lived opaque bearer (5 min TTL, `fp_oidc_<48 hex>` prefix).
    The bearer is then used in `Authorization: Bearer …` on the
    existing deploy routes.

    Gregale publishes the supported OAuth capability metadata at
    `/.well-known/oauth-authorization-server` (RFC 8414). That document
    advertises this token-exchange grant and intentionally omits
    unsupported authorization-code, refresh-token, and client-registration
    capabilities.

    The endpoint is anonymous — the JWT is the auth — so it does
    not require a session or a previous bearer. The first-use
    auto-create flow bootstraps a permissive trust policy on the
    `(account_id, issuer_url)` pair so customers do not have to
    configure the dashboard before their first CI deploy.

    The AuthLimit surface is the shared per-IP bucket (spec §11
    10/min/IP) — high-volume CI runners may hit the cap; long-lived
    deploy tokens remain the escape hatch.

    The endpoint also accepts the RFC 8693 OAuth 2.0 Token Exchange
    profile as `application/x-www-form-urlencoded`. That profile uses
    `subject_token` (a JWT), `subject_token_type` set to the registered
    JWT identifier, and one `audience`; it returns an opaque deploy bearer
    as an OAuth `access_token`. The legacy JSON shape remains supported.

    Args:
        body (OIDCExchangeRequest): Body for `POST /v1/auth/oidc/exchange` (ADR-101).
        body (OAuthTokenExchangeRequest): RFC 8693 form-encoded request profile for the OIDC
            exchange endpoint.
            Gregale requires one `audience` because it selects the account trust
            policy, accepts JWT subject tokens only, and issues only deploy:write
            access tokens. `resource` and actor-token delegation are unsupported.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OAuthTokenExchangeResponse | OIDCExchangeResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: OIDCExchangeRequest | OAuthTokenExchangeRequest | Unset = UNSET,
) -> OAuthTokenExchangeResponse | OIDCExchangeResponse | Problem | None:
    """Exchange an IdP-issued JWT for a short-lived deploy bearer

     ADR-101 / issue #270. CI runners that have an IdP-issued OIDC
    JWT (e.g. GitHub Actions `ACTIONS_ID_TOKEN_REQUEST_TOKEN`,
    GitLab CI, CircleCI) call this endpoint to exchange it for a
    short-lived opaque bearer (5 min TTL, `fp_oidc_<48 hex>` prefix).
    The bearer is then used in `Authorization: Bearer …` on the
    existing deploy routes.

    Gregale publishes the supported OAuth capability metadata at
    `/.well-known/oauth-authorization-server` (RFC 8414). That document
    advertises this token-exchange grant and intentionally omits
    unsupported authorization-code, refresh-token, and client-registration
    capabilities.

    The endpoint is anonymous — the JWT is the auth — so it does
    not require a session or a previous bearer. The first-use
    auto-create flow bootstraps a permissive trust policy on the
    `(account_id, issuer_url)` pair so customers do not have to
    configure the dashboard before their first CI deploy.

    The AuthLimit surface is the shared per-IP bucket (spec §11
    10/min/IP) — high-volume CI runners may hit the cap; long-lived
    deploy tokens remain the escape hatch.

    The endpoint also accepts the RFC 8693 OAuth 2.0 Token Exchange
    profile as `application/x-www-form-urlencoded`. That profile uses
    `subject_token` (a JWT), `subject_token_type` set to the registered
    JWT identifier, and one `audience`; it returns an opaque deploy bearer
    as an OAuth `access_token`. The legacy JSON shape remains supported.

    Args:
        body (OIDCExchangeRequest): Body for `POST /v1/auth/oidc/exchange` (ADR-101).
        body (OAuthTokenExchangeRequest): RFC 8693 form-encoded request profile for the OIDC
            exchange endpoint.
            Gregale requires one `audience` because it selects the account trust
            policy, accepts JWT subject tokens only, and issues only deploy:write
            access tokens. `resource` and actor-token delegation are unsupported.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OAuthTokenExchangeResponse | OIDCExchangeResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
