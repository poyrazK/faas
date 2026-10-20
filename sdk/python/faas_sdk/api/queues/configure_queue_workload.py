from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.queue_workload_profile_request import QueueWorkloadProfileRequest
from ...models.queue_workload_profile_response import QueueWorkloadProfileResponse
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    body: QueueWorkloadProfileRequest | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/apps/{slug}/queue-workload".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    if not isinstance(body, Unset):
        _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | QueueWorkloadProfileResponse | None:
    if response.status_code == 200:
        response_200 = QueueWorkloadProfileResponse.from_dict(response.json())

        return response_200

    if response.status_code == 201:
        response_201 = QueueWorkloadProfileResponse.from_dict(response.json())

        return response_201

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 402:
        response_402 = Problem.from_dict(response.json())

        return response_402

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | QueueWorkloadProfileResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: QueueWorkloadProfileRequest | Unset = UNSET,
) -> Response[Problem | QueueWorkloadProfileResponse]:
    """Configure the simple queue workload profile.

     Idempotently creates or updates the app's default push queue
    binding, consumer projection, and queue-depth scaling policy.
    Zero-valued request fields use platform defaults; advanced
    resources remain available through the individual APIs.

    Args:
        slug (str):
        body (QueueWorkloadProfileRequest | Unset): Declarative setup for the common queue worker
            profile. The server
            reconciles the default push binding, consumer projection, and
            queue-depth scaling policy as one idempotent operation.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | QueueWorkloadProfileResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: QueueWorkloadProfileRequest | Unset = UNSET,
) -> Problem | QueueWorkloadProfileResponse | None:
    """Configure the simple queue workload profile.

     Idempotently creates or updates the app's default push queue
    binding, consumer projection, and queue-depth scaling policy.
    Zero-valued request fields use platform defaults; advanced
    resources remain available through the individual APIs.

    Args:
        slug (str):
        body (QueueWorkloadProfileRequest | Unset): Declarative setup for the common queue worker
            profile. The server
            reconciles the default push binding, consumer projection, and
            queue-depth scaling policy as one idempotent operation.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | QueueWorkloadProfileResponse
    """

    return sync_detailed(
        slug=slug,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: QueueWorkloadProfileRequest | Unset = UNSET,
) -> Response[Problem | QueueWorkloadProfileResponse]:
    """Configure the simple queue workload profile.

     Idempotently creates or updates the app's default push queue
    binding, consumer projection, and queue-depth scaling policy.
    Zero-valued request fields use platform defaults; advanced
    resources remain available through the individual APIs.

    Args:
        slug (str):
        body (QueueWorkloadProfileRequest | Unset): Declarative setup for the common queue worker
            profile. The server
            reconciles the default push binding, consumer projection, and
            queue-depth scaling policy as one idempotent operation.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | QueueWorkloadProfileResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: QueueWorkloadProfileRequest | Unset = UNSET,
) -> Problem | QueueWorkloadProfileResponse | None:
    """Configure the simple queue workload profile.

     Idempotently creates or updates the app's default push queue
    binding, consumer projection, and queue-depth scaling policy.
    Zero-valued request fields use platform defaults; advanced
    resources remain available through the individual APIs.

    Args:
        slug (str):
        body (QueueWorkloadProfileRequest | Unset): Declarative setup for the common queue worker
            profile. The server
            reconciles the default push binding, consumer projection, and
            queue-depth scaling policy as one idempotent operation.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | QueueWorkloadProfileResponse
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
