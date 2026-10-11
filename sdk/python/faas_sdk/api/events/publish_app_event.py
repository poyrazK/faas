from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.app_publish_event_request import AppPublishEventRequest
from ...models.app_publish_event_response import AppPublishEventResponse
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
        "url": "/v1/apps/{slug}/events:publish".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AppPublishEventResponse | Problem | None:
    if response.status_code == 202:
        response_202 = AppPublishEventResponse.from_dict(response.json())

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

    if response.status_code == 413:
        response_413 = Problem.from_dict(response.json())

        return response_413

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

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
) -> Response[AppPublishEventResponse | Problem]:
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
) -> Response[AppPublishEventResponse | Problem]:
    """Publish with an application-scoped stable producer key.

     Requires an owned app, MFA and events:publish, deploy:write or admin.
    Source is app.<canonical-app-UUID>; event ID is key.<SHA-256 hex of key>.
    The key is exact, case-sensitive printable ASCII without spaces, 1..256 bytes.
    Identical type, schema version and JSON data return the original retained
    acceptance with duplicate=true, without extra fanout or storage charge.
    Changed content for the same app/key returns 409, including concurrent
    requests. Retained duplicate lookup precedes current schema admission.
    Occurrence time and trace context are first-publication metadata and do
    not change duplicate identity. Fanout uses normal account subscriptions
    matching the generated source; the publishing app is not the only consumer.
    Deduplication lasts while the normal receipt is retained: settled receipts
    become eligible for pruning after 30 days; holds can extend this window.
    After pruning the same key may be accepted again. App renames preserve
    identity; replacement apps have a new UUID and a new key namespace.
    No request-wide Idempotency-Key middleware is used. Retry uncertain outcomes
    with the same app, key and content. Acceptance does not mean delivery success.

    Args:
        slug (str):
        body (AppPublishEventRequest): Producer-key event publication with a 1 MiB total body
            limit.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppPublishEventResponse | Problem]
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
) -> AppPublishEventResponse | Problem | None:
    """Publish with an application-scoped stable producer key.

     Requires an owned app, MFA and events:publish, deploy:write or admin.
    Source is app.<canonical-app-UUID>; event ID is key.<SHA-256 hex of key>.
    The key is exact, case-sensitive printable ASCII without spaces, 1..256 bytes.
    Identical type, schema version and JSON data return the original retained
    acceptance with duplicate=true, without extra fanout or storage charge.
    Changed content for the same app/key returns 409, including concurrent
    requests. Retained duplicate lookup precedes current schema admission.
    Occurrence time and trace context are first-publication metadata and do
    not change duplicate identity. Fanout uses normal account subscriptions
    matching the generated source; the publishing app is not the only consumer.
    Deduplication lasts while the normal receipt is retained: settled receipts
    become eligible for pruning after 30 days; holds can extend this window.
    After pruning the same key may be accepted again. App renames preserve
    identity; replacement apps have a new UUID and a new key namespace.
    No request-wide Idempotency-Key middleware is used. Retry uncertain outcomes
    with the same app, key and content. Acceptance does not mean delivery success.

    Args:
        slug (str):
        body (AppPublishEventRequest): Producer-key event publication with a 1 MiB total body
            limit.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppPublishEventResponse | Problem
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
) -> Response[AppPublishEventResponse | Problem]:
    """Publish with an application-scoped stable producer key.

     Requires an owned app, MFA and events:publish, deploy:write or admin.
    Source is app.<canonical-app-UUID>; event ID is key.<SHA-256 hex of key>.
    The key is exact, case-sensitive printable ASCII without spaces, 1..256 bytes.
    Identical type, schema version and JSON data return the original retained
    acceptance with duplicate=true, without extra fanout or storage charge.
    Changed content for the same app/key returns 409, including concurrent
    requests. Retained duplicate lookup precedes current schema admission.
    Occurrence time and trace context are first-publication metadata and do
    not change duplicate identity. Fanout uses normal account subscriptions
    matching the generated source; the publishing app is not the only consumer.
    Deduplication lasts while the normal receipt is retained: settled receipts
    become eligible for pruning after 30 days; holds can extend this window.
    After pruning the same key may be accepted again. App renames preserve
    identity; replacement apps have a new UUID and a new key namespace.
    No request-wide Idempotency-Key middleware is used. Retry uncertain outcomes
    with the same app, key and content. Acceptance does not mean delivery success.

    Args:
        slug (str):
        body (AppPublishEventRequest): Producer-key event publication with a 1 MiB total body
            limit.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppPublishEventResponse | Problem]
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
) -> AppPublishEventResponse | Problem | None:
    """Publish with an application-scoped stable producer key.

     Requires an owned app, MFA and events:publish, deploy:write or admin.
    Source is app.<canonical-app-UUID>; event ID is key.<SHA-256 hex of key>.
    The key is exact, case-sensitive printable ASCII without spaces, 1..256 bytes.
    Identical type, schema version and JSON data return the original retained
    acceptance with duplicate=true, without extra fanout or storage charge.
    Changed content for the same app/key returns 409, including concurrent
    requests. Retained duplicate lookup precedes current schema admission.
    Occurrence time and trace context are first-publication metadata and do
    not change duplicate identity. Fanout uses normal account subscriptions
    matching the generated source; the publishing app is not the only consumer.
    Deduplication lasts while the normal receipt is retained: settled receipts
    become eligible for pruning after 30 days; holds can extend this window.
    After pruning the same key may be accepted again. App renames preserve
    identity; replacement apps have a new UUID and a new key namespace.
    No request-wide Idempotency-Key middleware is used. Retry uncertain outcomes
    with the same app, key and content. Acceptance does not mean delivery success.

    Args:
        slug (str):
        body (AppPublishEventRequest): Producer-key event publication with a 1 MiB total body
            limit.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppPublishEventResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
