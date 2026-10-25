from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.get_github_recovery_status_status import (
    GetGithubRecoveryStatusStatus,
)
from ...models.github_recovery_status_response import GithubRecoveryStatusResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    status: GetGithubRecoveryStatusStatus | Unset = "dead",
    limit: int | Unset = 100,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_status: str | Unset = UNSET
    if not isinstance(status, Unset):
        json_status = status

    params["status"] = json_status

    params["limit"] = limit

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/admin/ops/github/recovery",
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> GithubRecoveryStatusResponse | Problem | None:
    if response.status_code == 200:
        response_200 = GithubRecoveryStatusResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[GithubRecoveryStatusResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    status: GetGithubRecoveryStatusStatus | Unset = "dead",
    limit: int | Unset = 100,
) -> Response[GithubRecoveryStatusResponse | Problem]:
    """List operator-safe GitHub webhook and Check Run recovery queue items.

     Returns bounded queue metadata from githubd. Webhook payloads and
    installation credentials are deliberately excluded. Requires admin
    scope, MFA, and membership in FAAS_ADMIN_EMAILS.

    Args:
        status (GetGithubRecoveryStatusStatus | Unset):  Default: 'dead'.
        limit (int | Unset):  Default: 100.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[GithubRecoveryStatusResponse | Problem]
    """

    kwargs = _get_kwargs(
        status=status,
        limit=limit,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    status: GetGithubRecoveryStatusStatus | Unset = "dead",
    limit: int | Unset = 100,
) -> GithubRecoveryStatusResponse | Problem | None:
    """List operator-safe GitHub webhook and Check Run recovery queue items.

     Returns bounded queue metadata from githubd. Webhook payloads and
    installation credentials are deliberately excluded. Requires admin
    scope, MFA, and membership in FAAS_ADMIN_EMAILS.

    Args:
        status (GetGithubRecoveryStatusStatus | Unset):  Default: 'dead'.
        limit (int | Unset):  Default: 100.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        GithubRecoveryStatusResponse | Problem
    """

    return sync_detailed(
        client=client,
        status=status,
        limit=limit,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    status: GetGithubRecoveryStatusStatus | Unset = "dead",
    limit: int | Unset = 100,
) -> Response[GithubRecoveryStatusResponse | Problem]:
    """List operator-safe GitHub webhook and Check Run recovery queue items.

     Returns bounded queue metadata from githubd. Webhook payloads and
    installation credentials are deliberately excluded. Requires admin
    scope, MFA, and membership in FAAS_ADMIN_EMAILS.

    Args:
        status (GetGithubRecoveryStatusStatus | Unset):  Default: 'dead'.
        limit (int | Unset):  Default: 100.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[GithubRecoveryStatusResponse | Problem]
    """

    kwargs = _get_kwargs(
        status=status,
        limit=limit,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    status: GetGithubRecoveryStatusStatus | Unset = "dead",
    limit: int | Unset = 100,
) -> GithubRecoveryStatusResponse | Problem | None:
    """List operator-safe GitHub webhook and Check Run recovery queue items.

     Returns bounded queue metadata from githubd. Webhook payloads and
    installation credentials are deliberately excluded. Requires admin
    scope, MFA, and membership in FAAS_ADMIN_EMAILS.

    Args:
        status (GetGithubRecoveryStatusStatus | Unset):  Default: 'dead'.
        limit (int | Unset):  Default: 100.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        GithubRecoveryStatusResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            status=status,
            limit=limit,
        )
    ).parsed
