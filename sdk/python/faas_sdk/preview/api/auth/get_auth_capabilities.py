from http import HTTPStatus
from typing import Any, cast

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.auth_capabilities import AuthCapabilities
from ...types import Response


def _get_kwargs() -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/auth/capabilities",
    }

    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Any | AuthCapabilities | None:
    if response.status_code == 200:
        response_200 = AuthCapabilities.from_dict(response.json())

        return response_200

    if response.status_code == 302:
        response_302 = cast(Any, None)
        return response_302

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Any | AuthCapabilities]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
) -> Response[Any | AuthCapabilities]:
    r"""Sign-in OAuth capability signal for the dashboard.

     Returns the boot-resolved OAuth provider state for this apid
    host (issue #419 / ADR-046). The dashboard reads this on
    `/login` to decide whether to render the \"Sign in with
    Google\" / \"Sign in with GitHub\" buttons.

    Mounted behind the dashboard's session-cookie auth — a
    scanner without a session gets 302 to `/login` first, so
    this is not a brute-force amplification surface even though
    it surfaces provider enablement. The set of provider names
    is closed (`google`, `github`); future providers land as
    new keys, not by adding a list.

    `enabled=true` means the provider's `/v1/auth/<provider>`
    consent route will issue a 302 to the upstream consent
    screen on a fresh request. `enabled=false` means it will
    return 503 `oauth_provider_unavailable`.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | AuthCapabilities]
    """

    kwargs = _get_kwargs()

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
) -> Any | AuthCapabilities | None:
    r"""Sign-in OAuth capability signal for the dashboard.

     Returns the boot-resolved OAuth provider state for this apid
    host (issue #419 / ADR-046). The dashboard reads this on
    `/login` to decide whether to render the \"Sign in with
    Google\" / \"Sign in with GitHub\" buttons.

    Mounted behind the dashboard's session-cookie auth — a
    scanner without a session gets 302 to `/login` first, so
    this is not a brute-force amplification surface even though
    it surfaces provider enablement. The set of provider names
    is closed (`google`, `github`); future providers land as
    new keys, not by adding a list.

    `enabled=true` means the provider's `/v1/auth/<provider>`
    consent route will issue a 302 to the upstream consent
    screen on a fresh request. `enabled=false` means it will
    return 503 `oauth_provider_unavailable`.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | AuthCapabilities
    """

    return sync_detailed(
        client=client,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
) -> Response[Any | AuthCapabilities]:
    r"""Sign-in OAuth capability signal for the dashboard.

     Returns the boot-resolved OAuth provider state for this apid
    host (issue #419 / ADR-046). The dashboard reads this on
    `/login` to decide whether to render the \"Sign in with
    Google\" / \"Sign in with GitHub\" buttons.

    Mounted behind the dashboard's session-cookie auth — a
    scanner without a session gets 302 to `/login` first, so
    this is not a brute-force amplification surface even though
    it surfaces provider enablement. The set of provider names
    is closed (`google`, `github`); future providers land as
    new keys, not by adding a list.

    `enabled=true` means the provider's `/v1/auth/<provider>`
    consent route will issue a 302 to the upstream consent
    screen on a fresh request. `enabled=false` means it will
    return 503 `oauth_provider_unavailable`.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | AuthCapabilities]
    """

    kwargs = _get_kwargs()

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
) -> Any | AuthCapabilities | None:
    r"""Sign-in OAuth capability signal for the dashboard.

     Returns the boot-resolved OAuth provider state for this apid
    host (issue #419 / ADR-046). The dashboard reads this on
    `/login` to decide whether to render the \"Sign in with
    Google\" / \"Sign in with GitHub\" buttons.

    Mounted behind the dashboard's session-cookie auth — a
    scanner without a session gets 302 to `/login` first, so
    this is not a brute-force amplification surface even though
    it surfaces provider enablement. The set of provider names
    is closed (`google`, `github`); future providers land as
    new keys, not by adding a list.

    `enabled=true` means the provider's `/v1/auth/<provider>`
    consent route will issue a 302 to the upstream consent
    screen on a fresh request. `enabled=false` means it will
    return 503 `oauth_provider_unavailable`.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | AuthCapabilities
    """

    return (
        await asyncio_detailed(
            client=client,
        )
    ).parsed
