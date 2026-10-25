from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.git_hub_install_status import GitHubInstallStatus
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "delete",
        "url": "/v1/apps/{slug}/github".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> GitHubInstallStatus | Problem | None:
    if response.status_code == 200:
        response_200 = GitHubInstallStatus.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[GitHubInstallStatus | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    *,
    client: AuthenticatedClient,
    idempotency_key: str | Unset = UNSET,
) -> Response[GitHubInstallStatus | Problem]:
    """Remove an app's GitHub repository binding.

     Bearer API-key surface for customer automation. Requires
    `github:manage`; the account-level GitHub installation remains
    available for a later bind. Safe to retry with an idempotency key.

    Args:
        slug (str):
        idempotency_key (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[GitHubInstallStatus | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient,
    idempotency_key: str | Unset = UNSET,
) -> GitHubInstallStatus | Problem | None:
    """Remove an app's GitHub repository binding.

     Bearer API-key surface for customer automation. Requires
    `github:manage`; the account-level GitHub installation remains
    available for a later bind. Safe to retry with an idempotency key.

    Args:
        slug (str):
        idempotency_key (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        GitHubInstallStatus | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient,
    idempotency_key: str | Unset = UNSET,
) -> Response[GitHubInstallStatus | Problem]:
    """Remove an app's GitHub repository binding.

     Bearer API-key surface for customer automation. Requires
    `github:manage`; the account-level GitHub installation remains
    available for a later bind. Safe to retry with an idempotency key.

    Args:
        slug (str):
        idempotency_key (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[GitHubInstallStatus | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient,
    idempotency_key: str | Unset = UNSET,
) -> GitHubInstallStatus | Problem | None:
    """Remove an app's GitHub repository binding.

     Bearer API-key surface for customer automation. Requires
    `github:manage`; the account-level GitHub installation remains
    available for a later bind. Safe to retry with an idempotency key.

    Args:
        slug (str):
        idempotency_key (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        GitHubInstallStatus | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            idempotency_key=idempotency_key,
        )
    ).parsed
