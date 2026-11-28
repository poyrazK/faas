from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.application_standard_assignment import ApplicationStandardAssignment
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    assignment: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/orgs/{slug}/application-standard-assignments/{assignment}".format(
            slug=quote(str(slug), safe=""),
            assignment=quote(str(assignment), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ApplicationStandardAssignment | Problem | None:
    if response.status_code == 200:
        response_200 = ApplicationStandardAssignment.from_dict(response.json())

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
) -> Response[ApplicationStandardAssignment | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    assignment: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[ApplicationStandardAssignment | Problem]:
    """Read a retained assignment and its current revision

     Returns the retained assignment even when inactive. Its admission version governs new services;
    application adoption and consumer progress remain separate. Use its current revision to preview an
    update, deactivation or rollback. Available while mutations are disabled; requires organization
    membership, read scope and completed MFA.

    Args:
        slug (str):
        assignment (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ApplicationStandardAssignment | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        assignment=assignment,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    assignment: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> ApplicationStandardAssignment | Problem | None:
    """Read a retained assignment and its current revision

     Returns the retained assignment even when inactive. Its admission version governs new services;
    application adoption and consumer progress remain separate. Use its current revision to preview an
    update, deactivation or rollback. Available while mutations are disabled; requires organization
    membership, read scope and completed MFA.

    Args:
        slug (str):
        assignment (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ApplicationStandardAssignment | Problem
    """

    return sync_detailed(
        slug=slug,
        assignment=assignment,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    assignment: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[ApplicationStandardAssignment | Problem]:
    """Read a retained assignment and its current revision

     Returns the retained assignment even when inactive. Its admission version governs new services;
    application adoption and consumer progress remain separate. Use its current revision to preview an
    update, deactivation or rollback. Available while mutations are disabled; requires organization
    membership, read scope and completed MFA.

    Args:
        slug (str):
        assignment (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ApplicationStandardAssignment | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        assignment=assignment,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    assignment: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> ApplicationStandardAssignment | Problem | None:
    """Read a retained assignment and its current revision

     Returns the retained assignment even when inactive. Its admission version governs new services;
    application adoption and consumer progress remain separate. Use its current revision to preview an
    update, deactivation or rollback. Available while mutations are disabled; requires organization
    membership, read scope and completed MFA.

    Args:
        slug (str):
        assignment (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ApplicationStandardAssignment | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            assignment=assignment,
            client=client,
        )
    ).parsed
