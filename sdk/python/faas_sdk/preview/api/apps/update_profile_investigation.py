from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.profile_investigation_response import ProfileInvestigationResponse
from ...models.save_profile_investigation_request import SaveProfileInvestigationRequest
from ...types import Response


def _get_kwargs(
    slug: str,
    id: UUID,
    *,
    body: SaveProfileInvestigationRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/apps/{slug}/profiles/investigations/{id}".format(
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | ProfileInvestigationResponse | None:
    if response.status_code == 200:
        response_200 = ProfileInvestigationResponse.from_dict(response.json())

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
) -> Response[Problem | ProfileInvestigationResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: SaveProfileInvestigationRequest,
) -> Response[Problem | ProfileInvestigationResponse]:
    """Update a saved profiling investigation using its current revision.

     Replaces metadata using the current revision and deploy-write access. Unchanged expired selections
    permit commentary edits; changed selections recheck profiling entitlement, retention and deployment
    ownership. Concurrent edits return 409. Saving does not extend profile retention.

    Args:
        slug (str):
        id (UUID):
        body (SaveProfileInvestigationRequest): Complete replacement. Supply expected_revision 0
            for creation or the current revision for updates. Concurrent changes return conflict. Body
            limited to 65536 bytes.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ProfileInvestigationResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: SaveProfileInvestigationRequest,
) -> Problem | ProfileInvestigationResponse | None:
    """Update a saved profiling investigation using its current revision.

     Replaces metadata using the current revision and deploy-write access. Unchanged expired selections
    permit commentary edits; changed selections recheck profiling entitlement, retention and deployment
    ownership. Concurrent edits return 409. Saving does not extend profile retention.

    Args:
        slug (str):
        id (UUID):
        body (SaveProfileInvestigationRequest): Complete replacement. Supply expected_revision 0
            for creation or the current revision for updates. Concurrent changes return conflict. Body
            limited to 65536 bytes.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ProfileInvestigationResponse
    """

    return sync_detailed(
        slug=slug,
        id=id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: SaveProfileInvestigationRequest,
) -> Response[Problem | ProfileInvestigationResponse]:
    """Update a saved profiling investigation using its current revision.

     Replaces metadata using the current revision and deploy-write access. Unchanged expired selections
    permit commentary edits; changed selections recheck profiling entitlement, retention and deployment
    ownership. Concurrent edits return 409. Saving does not extend profile retention.

    Args:
        slug (str):
        id (UUID):
        body (SaveProfileInvestigationRequest): Complete replacement. Supply expected_revision 0
            for creation or the current revision for updates. Concurrent changes return conflict. Body
            limited to 65536 bytes.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ProfileInvestigationResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: SaveProfileInvestigationRequest,
) -> Problem | ProfileInvestigationResponse | None:
    """Update a saved profiling investigation using its current revision.

     Replaces metadata using the current revision and deploy-write access. Unchanged expired selections
    permit commentary edits; changed selections recheck profiling entitlement, retention and deployment
    ownership. Concurrent edits return 409. Saving does not extend profile retention.

    Args:
        slug (str):
        id (UUID):
        body (SaveProfileInvestigationRequest): Complete replacement. Supply expected_revision 0
            for creation or the current revision for updates. Concurrent changes return conflict. Body
            limited to 65536 bytes.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ProfileInvestigationResponse
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            client=client,
            body=body,
        )
    ).parsed
