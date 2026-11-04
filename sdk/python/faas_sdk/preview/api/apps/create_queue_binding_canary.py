from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.create_queue_binding_canary_request import CreateQueueBindingCanaryRequest
from ...models.problem import Problem
from ...models.queue_binding_canary import QueueBindingCanary
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: CreateQueueBindingCanaryRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/bindings/queue-canaries".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | QueueBindingCanary | None:
    if response.status_code == 201:
        response_201 = QueueBindingCanary.from_dict(response.json())

        return response_201

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

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | QueueBindingCanary]:
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
    body: CreateQueueBindingCanaryRequest,
) -> Response[Problem | QueueBindingCanary]:
    """Publish one explicitly consented safe message through a push queue binding.

     Requires a live pinned deployment declaring this binding in GREGALE_QUEUE_CANARY_BINDINGS. Uses
    normal queue and runtime capacity; no traffic change.

    Args:
        slug (str):
        body (CreateQueueBindingCanaryRequest): Publication consent and deployment selection for a
            bounded push queue canary.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | QueueBindingCanary]
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
    body: CreateQueueBindingCanaryRequest,
) -> Problem | QueueBindingCanary | None:
    """Publish one explicitly consented safe message through a push queue binding.

     Requires a live pinned deployment declaring this binding in GREGALE_QUEUE_CANARY_BINDINGS. Uses
    normal queue and runtime capacity; no traffic change.

    Args:
        slug (str):
        body (CreateQueueBindingCanaryRequest): Publication consent and deployment selection for a
            bounded push queue canary.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | QueueBindingCanary
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
    body: CreateQueueBindingCanaryRequest,
) -> Response[Problem | QueueBindingCanary]:
    """Publish one explicitly consented safe message through a push queue binding.

     Requires a live pinned deployment declaring this binding in GREGALE_QUEUE_CANARY_BINDINGS. Uses
    normal queue and runtime capacity; no traffic change.

    Args:
        slug (str):
        body (CreateQueueBindingCanaryRequest): Publication consent and deployment selection for a
            bounded push queue canary.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | QueueBindingCanary]
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
    body: CreateQueueBindingCanaryRequest,
) -> Problem | QueueBindingCanary | None:
    """Publish one explicitly consented safe message through a push queue binding.

     Requires a live pinned deployment declaring this binding in GREGALE_QUEUE_CANARY_BINDINGS. Uses
    normal queue and runtime capacity; no traffic change.

    Args:
        slug (str):
        body (CreateQueueBindingCanaryRequest): Publication consent and deployment selection for a
            bounded push queue canary.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | QueueBindingCanary
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
