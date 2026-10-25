from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.commit_source_response import CommitSourceResponse
from ...models.problem import Problem
from ...models.set_commit_source_enabled_body import SetCommitSourceEnabledBody
from ...types import Response


def _get_kwargs(
    source: UUID,
    *,
    body: SetCommitSourceEnabledBody,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "patch",
        "url": "/v1/commit-sources/{source}".format(
            source=quote(str(source), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> CommitSourceResponse | Problem | None:
    if response.status_code == 200:
        response_200 = CommitSourceResponse.from_dict(response.json())

        return response_200

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


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[CommitSourceResponse | Problem]:
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
    body: SetCommitSourceEnabledBody,
) -> Response[CommitSourceResponse | Problem]:
    """Pause or resume new source acceptance; existing receipts remain recoverable.

    Args:
        source (UUID):
        body (SetCommitSourceEnabledBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[CommitSourceResponse | Problem]
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
    body: SetCommitSourceEnabledBody,
) -> CommitSourceResponse | Problem | None:
    """Pause or resume new source acceptance; existing receipts remain recoverable.

    Args:
        source (UUID):
        body (SetCommitSourceEnabledBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        CommitSourceResponse | Problem
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
    body: SetCommitSourceEnabledBody,
) -> Response[CommitSourceResponse | Problem]:
    """Pause or resume new source acceptance; existing receipts remain recoverable.

    Args:
        source (UUID):
        body (SetCommitSourceEnabledBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[CommitSourceResponse | Problem]
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
    body: SetCommitSourceEnabledBody,
) -> CommitSourceResponse | Problem | None:
    """Pause or resume new source acceptance; existing receipts remain recoverable.

    Args:
        source (UUID):
        body (SetCommitSourceEnabledBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        CommitSourceResponse | Problem
    """

    return (
        await asyncio_detailed(
            source=source,
            client=client,
            body=body,
        )
    ).parsed
