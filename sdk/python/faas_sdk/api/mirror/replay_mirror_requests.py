from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.mirror_replay_batch_request import MirrorReplayBatchRequest
from ...models.mirror_replay_batch_response import MirrorReplayBatchResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    id: str,
    *,
    body: MirrorReplayBatchRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/mirrors/{id}/replay".format(
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
) -> MirrorReplayBatchResponse | Problem | None:
    if response.status_code == 202:
        response_202 = MirrorReplayBatchResponse.from_dict(response.json())

        return response_202

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

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[MirrorReplayBatchResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: MirrorReplayBatchRequest,
) -> Response[MirrorReplayBatchResponse | Problem]:
    """Replay a sanitized historical request corpus against the mirror deployment.

     Queues 1–100 caller-sanitized JSON requests against this rule's mirror
    deployment. Gregale strips credential, platform-owned, hop-by-hop, and
    rule-configured redact headers again before enqueueing. GET/HEAD/OPTIONS
    are accepted by default; POST/PUT/PATCH/DELETE require the explicit
    `allow_unsafe_methods` acknowledgement. Source response bodies are never
    uploaded: an optional SHA-256 expectation enables body comparison.

    Args:
        slug (str):
        id (str):
        body (MirrorReplayBatchRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[MirrorReplayBatchResponse | Problem]
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
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: MirrorReplayBatchRequest,
) -> MirrorReplayBatchResponse | Problem | None:
    """Replay a sanitized historical request corpus against the mirror deployment.

     Queues 1–100 caller-sanitized JSON requests against this rule's mirror
    deployment. Gregale strips credential, platform-owned, hop-by-hop, and
    rule-configured redact headers again before enqueueing. GET/HEAD/OPTIONS
    are accepted by default; POST/PUT/PATCH/DELETE require the explicit
    `allow_unsafe_methods` acknowledgement. Source response bodies are never
    uploaded: an optional SHA-256 expectation enables body comparison.

    Args:
        slug (str):
        id (str):
        body (MirrorReplayBatchRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        MirrorReplayBatchResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        id=id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: MirrorReplayBatchRequest,
) -> Response[MirrorReplayBatchResponse | Problem]:
    """Replay a sanitized historical request corpus against the mirror deployment.

     Queues 1–100 caller-sanitized JSON requests against this rule's mirror
    deployment. Gregale strips credential, platform-owned, hop-by-hop, and
    rule-configured redact headers again before enqueueing. GET/HEAD/OPTIONS
    are accepted by default; POST/PUT/PATCH/DELETE require the explicit
    `allow_unsafe_methods` acknowledgement. Source response bodies are never
    uploaded: an optional SHA-256 expectation enables body comparison.

    Args:
        slug (str):
        id (str):
        body (MirrorReplayBatchRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[MirrorReplayBatchResponse | Problem]
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
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: MirrorReplayBatchRequest,
) -> MirrorReplayBatchResponse | Problem | None:
    """Replay a sanitized historical request corpus against the mirror deployment.

     Queues 1–100 caller-sanitized JSON requests against this rule's mirror
    deployment. Gregale strips credential, platform-owned, hop-by-hop, and
    rule-configured redact headers again before enqueueing. GET/HEAD/OPTIONS
    are accepted by default; POST/PUT/PATCH/DELETE require the explicit
    `allow_unsafe_methods` acknowledgement. Source response bodies are never
    uploaded: an optional SHA-256 expectation enables body comparison.

    Args:
        slug (str):
        id (str):
        body (MirrorReplayBatchRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        MirrorReplayBatchResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            client=client,
            body=body,
        )
    ).parsed
