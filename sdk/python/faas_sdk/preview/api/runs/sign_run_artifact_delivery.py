from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.object_signed_request import ObjectSignedRequest
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    route: str,
    delivery: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/run-artifact-deliveries/{route}/{delivery}/download-url".format(
            slug=quote(str(slug), safe=""),
            route=quote(str(route), safe=""),
            delivery=quote(str(delivery), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | ObjectSignedRequest | Problem | None:
    if response.status_code == 200:
        response_200 = ObjectSignedRequest.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 402:
        response_402 = cast(Any, None)
        return response_402

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = cast(Any, None)
        return response_409

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if response.status_code == 503:
        response_503 = cast(Any, None)
        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Any | ObjectSignedRequest | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    route: str,
    delivery: UUID,
    *,
    client: AuthenticatedClient,
) -> Response[Any | ObjectSignedRequest | Problem]:
    """Issue a short-lived app-owned report download capability

     Requires current app and bucket read authority and a completed delivery. Uses existing branded
    object GET capabilities with a five-minute expiry and the retained public version when available.
    Applications authenticate their customers before issuing a URL and keep it out of Agent transcripts.

    Args:
        slug (str):
        route (str):
        delivery (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ObjectSignedRequest | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        route=route,
        delivery=delivery,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    route: str,
    delivery: UUID,
    *,
    client: AuthenticatedClient,
) -> Any | ObjectSignedRequest | Problem | None:
    """Issue a short-lived app-owned report download capability

     Requires current app and bucket read authority and a completed delivery. Uses existing branded
    object GET capabilities with a five-minute expiry and the retained public version when available.
    Applications authenticate their customers before issuing a URL and keep it out of Agent transcripts.

    Args:
        slug (str):
        route (str):
        delivery (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ObjectSignedRequest | Problem
    """

    return sync_detailed(
        slug=slug,
        route=route,
        delivery=delivery,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    route: str,
    delivery: UUID,
    *,
    client: AuthenticatedClient,
) -> Response[Any | ObjectSignedRequest | Problem]:
    """Issue a short-lived app-owned report download capability

     Requires current app and bucket read authority and a completed delivery. Uses existing branded
    object GET capabilities with a five-minute expiry and the retained public version when available.
    Applications authenticate their customers before issuing a URL and keep it out of Agent transcripts.

    Args:
        slug (str):
        route (str):
        delivery (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ObjectSignedRequest | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        route=route,
        delivery=delivery,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    route: str,
    delivery: UUID,
    *,
    client: AuthenticatedClient,
) -> Any | ObjectSignedRequest | Problem | None:
    """Issue a short-lived app-owned report download capability

     Requires current app and bucket read authority and a completed delivery. Uses existing branded
    object GET capabilities with a five-minute expiry and the retained public version when available.
    Applications authenticate their customers before issuing a URL and keep it out of Agent transcripts.

    Args:
        slug (str):
        route (str):
        delivery (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ObjectSignedRequest | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            route=route,
            delivery=delivery,
            client=client,
        )
    ).parsed
