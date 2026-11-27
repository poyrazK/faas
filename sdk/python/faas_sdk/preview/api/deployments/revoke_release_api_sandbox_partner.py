from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    deployment: UUID,
    sandbox: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "delete",
        "url": "/v1/apps/{slug}/deployments/{deployment}/api-sandboxes/{sandbox}/partner".format(
            slug=quote(str(slug), safe=""),
            deployment=quote(str(deployment), safe=""),
            sandbox=quote(str(sandbox), safe=""),
        ),
    }

    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Any | Problem | None:
    if response.status_code == 204:
        response_204 = cast(Any, None)
        return response_204

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


def _build_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Response[Any | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    deployment: UUID,
    sandbox: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Any | Problem]:
    """Revoke this partner copy's invite and redeemed key.

    Args:
        slug (str):
        deployment (UUID):
        sandbox (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        deployment=deployment,
        sandbox=sandbox,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    deployment: UUID,
    sandbox: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Any | Problem | None:
    """Revoke this partner copy's invite and redeemed key.

    Args:
        slug (str):
        deployment (UUID):
        sandbox (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return sync_detailed(
        slug=slug,
        deployment=deployment,
        sandbox=sandbox,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    deployment: UUID,
    sandbox: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Any | Problem]:
    """Revoke this partner copy's invite and redeemed key.

    Args:
        slug (str):
        deployment (UUID):
        sandbox (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        deployment=deployment,
        sandbox=sandbox,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    deployment: UUID,
    sandbox: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Any | Problem | None:
    """Revoke this partner copy's invite and redeemed key.

    Args:
        slug (str):
        deployment (UUID):
        sandbox (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            deployment=deployment,
            sandbox=sandbox,
            client=client,
        )
    ).parsed
