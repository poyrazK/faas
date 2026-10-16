from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.application_standard_review import ApplicationStandardReview
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    review: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/orgs/{slug}/application-standard-reviews/{review}".format(
            slug=quote(str(slug), safe=""),
            review=quote(str(review), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ApplicationStandardReview | Problem | None:
    if response.status_code == 200:
        response_200 = ApplicationStandardReview.from_dict(response.json())

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
) -> Response[ApplicationStandardReview | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    review: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[ApplicationStandardReview | Problem]:
    """Inspect a saved review, including expiry and blockers

    Args:
        slug (str):
        review (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ApplicationStandardReview | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        review=review,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    review: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> ApplicationStandardReview | Problem | None:
    """Inspect a saved review, including expiry and blockers

    Args:
        slug (str):
        review (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ApplicationStandardReview | Problem
    """

    return sync_detailed(
        slug=slug,
        review=review,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    review: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[ApplicationStandardReview | Problem]:
    """Inspect a saved review, including expiry and blockers

    Args:
        slug (str):
        review (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ApplicationStandardReview | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        review=review,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    review: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> ApplicationStandardReview | Problem | None:
    """Inspect a saved review, including expiry and blockers

    Args:
        slug (str):
        review (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ApplicationStandardReview | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            review=review,
            client=client,
        )
    ).parsed
