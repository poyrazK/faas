from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.project_rollback_operation import ProjectRollbackOperation
from ...types import Response


def _get_kwargs(
    slug: str,
    operation: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/projects/{slug}/rollbacks/{operation}".format(
            slug=quote(str(slug), safe=""),
            operation=quote(str(operation), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | ProjectRollbackOperation | None:
    if response.status_code == 200:
        response_200 = ProjectRollbackOperation.from_dict(response.json())

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

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

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
) -> Response[Problem | ProjectRollbackOperation]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    operation: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Problem | ProjectRollbackOperation]:
    """Read a durable project rollback receipt.

    Args:
        slug (str):
        operation (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ProjectRollbackOperation]
    """

    kwargs = _get_kwargs(
        slug=slug,
        operation=operation,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    operation: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Problem | ProjectRollbackOperation | None:
    """Read a durable project rollback receipt.

    Args:
        slug (str):
        operation (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ProjectRollbackOperation
    """

    return sync_detailed(
        slug=slug,
        operation=operation,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    operation: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Problem | ProjectRollbackOperation]:
    """Read a durable project rollback receipt.

    Args:
        slug (str):
        operation (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ProjectRollbackOperation]
    """

    kwargs = _get_kwargs(
        slug=slug,
        operation=operation,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    operation: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Problem | ProjectRollbackOperation | None:
    """Read a durable project rollback receipt.

    Args:
        slug (str):
        operation (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ProjectRollbackOperation
    """

    return (
        await asyncio_detailed(
            slug=slug,
            operation=operation,
            client=client,
        )
    ).parsed
