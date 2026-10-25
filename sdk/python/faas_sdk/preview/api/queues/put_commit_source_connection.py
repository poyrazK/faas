from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.put_commit_source_connection_body import PutCommitSourceConnectionBody
from ...types import Response


def _get_kwargs(
    source: UUID,
    *,
    body: PutCommitSourceConnectionBody,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/commit-sources/{source}/connection".format(
            source=quote(str(source), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Any | Problem | None:
    if response.status_code == 204:
        response_204 = cast(Any, None)
        return response_204

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

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


def _build_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Response[Any | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    source: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: PutCommitSourceConnectionBody,
) -> Response[Any | Problem]:
    """Seal a verified-TLS database connection; credentials are never returned.

    Args:
        source (UUID):
        body (PutCommitSourceConnectionBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        source=source,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    source: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: PutCommitSourceConnectionBody,
) -> Any | Problem | None:
    """Seal a verified-TLS database connection; credentials are never returned.

    Args:
        source (UUID):
        body (PutCommitSourceConnectionBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return sync_detailed(
        source=source,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    source: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: PutCommitSourceConnectionBody,
) -> Response[Any | Problem]:
    """Seal a verified-TLS database connection; credentials are never returned.

    Args:
        source (UUID):
        body (PutCommitSourceConnectionBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        source=source,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    source: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: PutCommitSourceConnectionBody,
) -> Any | Problem | None:
    """Seal a verified-TLS database connection; credentials are never returned.

    Args:
        source (UUID):
        body (PutCommitSourceConnectionBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return (
        await asyncio_detailed(
            source=source,
            client=client,
            body=body,
        )
    ).parsed
