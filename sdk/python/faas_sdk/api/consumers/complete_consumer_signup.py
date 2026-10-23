from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.complete_consumer_signup_request import CompleteConsumerSignupRequest
from ...models.complete_consumer_signup_response import CompleteConsumerSignupResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    signup: UUID,
    *,
    body: CompleteConsumerSignupRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/consumer-signup/{slug}/signups/{signup}/complete".format(
            slug=quote(str(slug), safe=""),
            signup=quote(str(signup), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> CompleteConsumerSignupResponse | Problem | None:
    if response.status_code == 200:
        response_200 = CompleteConsumerSignupResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 410:
        response_410 = Problem.from_dict(response.json())

        return response_410

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if response.status_code == 502:
        response_502 = Problem.from_dict(response.json())

        return response_502

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[CompleteConsumerSignupResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    signup: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: CompleteConsumerSignupRequest,
) -> Response[CompleteConsumerSignupResponse | Problem]:
    """Complete a sign-up once the card is saved and get a consumer key.

     Needs no credential: the claim token proves the sign-up. Each call
    returns a new key and revokes the one the sign-up issued before.

    Args:
        slug (str):
        signup (UUID):
        body (CompleteConsumerSignupRequest): Completes a sign-up.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[CompleteConsumerSignupResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        signup=signup,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    signup: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: CompleteConsumerSignupRequest,
) -> CompleteConsumerSignupResponse | Problem | None:
    """Complete a sign-up once the card is saved and get a consumer key.

     Needs no credential: the claim token proves the sign-up. Each call
    returns a new key and revokes the one the sign-up issued before.

    Args:
        slug (str):
        signup (UUID):
        body (CompleteConsumerSignupRequest): Completes a sign-up.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        CompleteConsumerSignupResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        signup=signup,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    signup: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: CompleteConsumerSignupRequest,
) -> Response[CompleteConsumerSignupResponse | Problem]:
    """Complete a sign-up once the card is saved and get a consumer key.

     Needs no credential: the claim token proves the sign-up. Each call
    returns a new key and revokes the one the sign-up issued before.

    Args:
        slug (str):
        signup (UUID):
        body (CompleteConsumerSignupRequest): Completes a sign-up.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[CompleteConsumerSignupResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        signup=signup,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    signup: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: CompleteConsumerSignupRequest,
) -> CompleteConsumerSignupResponse | Problem | None:
    """Complete a sign-up once the card is saved and get a consumer key.

     Needs no credential: the claim token proves the sign-up. Each call
    returns a new key and revokes the one the sign-up issued before.

    Args:
        slug (str):
        signup (UUID):
        body (CompleteConsumerSignupRequest): Completes a sign-up.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        CompleteConsumerSignupResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            signup=signup,
            client=client,
            body=body,
        )
    ).parsed
