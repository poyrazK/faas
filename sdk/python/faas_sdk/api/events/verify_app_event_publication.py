from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.app_event_publication_verification import AppEventPublicationVerification
from ...models.app_publish_event_request import AppPublishEventRequest
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: AppPublishEventRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/events/verify-publication".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AppEventPublicationVerification | Problem | None:
    if response.status_code == 200:
        response_200 = AppEventPublicationVerification.from_dict(response.json())

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

    if response.status_code == 413:
        response_413 = Problem.from_dict(response.json())

        return response_413

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if response.status_code == 500:
        response_500 = Problem.from_dict(response.json())

        return response_500

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[AppEventPublicationVerification | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    *,
    client: AuthenticatedClient,
    body: AppPublishEventRequest,
) -> Response[AppEventPublicationVerification | Problem]:
    """Compare intended publication content with retained acceptance.

     Read-only despite POST: requires an owned app, apps:read/admin, MFA and rate limits.
    Accepts the original app publish body within its existing 1 MiB limit and
    derives the same stable app/key identity. Compares normalized type, schema
    version and semantic JSON data using the ordinary publication identity rules.
    Occurrence time and trace metadata are excluded. Current schema admission
    rules do not invalidate verification of previously accepted content.
    Returns match, conflict or unavailable with HTTP 200. Match and conflict
    include the retained original receipt from the same comparison snapshot.
    Conflict is an observation, not a publish rejection. Unavailable cannot
    distinguish never accepted, pruning or concurrent acceptance not yet visible.
    No append, fanout, lease, replay, retention refresh or idempotency response
    cache occurs. Neither match nor conflict establishes consumer execution.
    Request and stored payloads and raw producer keys are not returned.
    Read errors remain errors rather than being reported as unavailable.
    Query parameters are rejected; use the existing status read for consumer evidence.

    Args:
        slug (str):
        body (AppPublishEventRequest): Producer-key event publication with a 1 MiB total body
            limit.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppEventPublicationVerification | Problem]
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
    client: AuthenticatedClient,
    body: AppPublishEventRequest,
) -> AppEventPublicationVerification | Problem | None:
    """Compare intended publication content with retained acceptance.

     Read-only despite POST: requires an owned app, apps:read/admin, MFA and rate limits.
    Accepts the original app publish body within its existing 1 MiB limit and
    derives the same stable app/key identity. Compares normalized type, schema
    version and semantic JSON data using the ordinary publication identity rules.
    Occurrence time and trace metadata are excluded. Current schema admission
    rules do not invalidate verification of previously accepted content.
    Returns match, conflict or unavailable with HTTP 200. Match and conflict
    include the retained original receipt from the same comparison snapshot.
    Conflict is an observation, not a publish rejection. Unavailable cannot
    distinguish never accepted, pruning or concurrent acceptance not yet visible.
    No append, fanout, lease, replay, retention refresh or idempotency response
    cache occurs. Neither match nor conflict establishes consumer execution.
    Request and stored payloads and raw producer keys are not returned.
    Read errors remain errors rather than being reported as unavailable.
    Query parameters are rejected; use the existing status read for consumer evidence.

    Args:
        slug (str):
        body (AppPublishEventRequest): Producer-key event publication with a 1 MiB total body
            limit.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppEventPublicationVerification | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient,
    body: AppPublishEventRequest,
) -> Response[AppEventPublicationVerification | Problem]:
    """Compare intended publication content with retained acceptance.

     Read-only despite POST: requires an owned app, apps:read/admin, MFA and rate limits.
    Accepts the original app publish body within its existing 1 MiB limit and
    derives the same stable app/key identity. Compares normalized type, schema
    version and semantic JSON data using the ordinary publication identity rules.
    Occurrence time and trace metadata are excluded. Current schema admission
    rules do not invalidate verification of previously accepted content.
    Returns match, conflict or unavailable with HTTP 200. Match and conflict
    include the retained original receipt from the same comparison snapshot.
    Conflict is an observation, not a publish rejection. Unavailable cannot
    distinguish never accepted, pruning or concurrent acceptance not yet visible.
    No append, fanout, lease, replay, retention refresh or idempotency response
    cache occurs. Neither match nor conflict establishes consumer execution.
    Request and stored payloads and raw producer keys are not returned.
    Read errors remain errors rather than being reported as unavailable.
    Query parameters are rejected; use the existing status read for consumer evidence.

    Args:
        slug (str):
        body (AppPublishEventRequest): Producer-key event publication with a 1 MiB total body
            limit.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppEventPublicationVerification | Problem]
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
    client: AuthenticatedClient,
    body: AppPublishEventRequest,
) -> AppEventPublicationVerification | Problem | None:
    """Compare intended publication content with retained acceptance.

     Read-only despite POST: requires an owned app, apps:read/admin, MFA and rate limits.
    Accepts the original app publish body within its existing 1 MiB limit and
    derives the same stable app/key identity. Compares normalized type, schema
    version and semantic JSON data using the ordinary publication identity rules.
    Occurrence time and trace metadata are excluded. Current schema admission
    rules do not invalidate verification of previously accepted content.
    Returns match, conflict or unavailable with HTTP 200. Match and conflict
    include the retained original receipt from the same comparison snapshot.
    Conflict is an observation, not a publish rejection. Unavailable cannot
    distinguish never accepted, pruning or concurrent acceptance not yet visible.
    No append, fanout, lease, replay, retention refresh or idempotency response
    cache occurs. Neither match nor conflict establishes consumer execution.
    Request and stored payloads and raw producer keys are not returned.
    Read errors remain errors rather than being reported as unavailable.
    Query parameters are rejected; use the existing status read for consumer evidence.

    Args:
        slug (str):
        body (AppPublishEventRequest): Producer-key event publication with a 1 MiB total body
            limit.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppEventPublicationVerification | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
