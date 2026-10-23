from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.data_api_change_event_batch import DataAPIChangeEventBatch
from ...models.data_api_change_event_batch_response import DataAPIChangeEventBatchResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    *,
    body: DataAPIChangeEventBatch,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/runtime/data-api/events",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> DataAPIChangeEventBatchResponse | Problem | None:
    if response.status_code == 200:
        response_200 = DataAPIChangeEventBatchResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 413:
        response_413 = Problem.from_dict(response.json())

        return response_413

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[DataAPIChangeEventBatchResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient,
    body: DataAPIChangeEventBatch,
) -> Response[DataAPIChangeEventBatchResponse | Problem]:
    """Publish recorded Data API row changes

     Private runtime surface for change hooks (ADR-20261022121642). Requires a short-lived gregale:data-
    api:events workload assertion from a live instance of an active app; account and app come from it,
    never from the body. Each event publishes as an app-scoped producer-key event under the app's own
    source with key data-api.change.ID, so a republished change deduplicates while its receipt is
    retained. Events publish in order; publication stops at the first event that may succeed later, and
    later events are reported as retryable without being attempted. Owner API keys and application JWTs
    are not accepted.

    Args:
        body (DataAPIChangeEventBatch): A runtime's private publication of recorded changes, in
            recorded order.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DataAPIChangeEventBatchResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient,
    body: DataAPIChangeEventBatch,
) -> DataAPIChangeEventBatchResponse | Problem | None:
    """Publish recorded Data API row changes

     Private runtime surface for change hooks (ADR-20261022121642). Requires a short-lived gregale:data-
    api:events workload assertion from a live instance of an active app; account and app come from it,
    never from the body. Each event publishes as an app-scoped producer-key event under the app's own
    source with key data-api.change.ID, so a republished change deduplicates while its receipt is
    retained. Events publish in order; publication stops at the first event that may succeed later, and
    later events are reported as retryable without being attempted. Owner API keys and application JWTs
    are not accepted.

    Args:
        body (DataAPIChangeEventBatch): A runtime's private publication of recorded changes, in
            recorded order.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DataAPIChangeEventBatchResponse | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient,
    body: DataAPIChangeEventBatch,
) -> Response[DataAPIChangeEventBatchResponse | Problem]:
    """Publish recorded Data API row changes

     Private runtime surface for change hooks (ADR-20261022121642). Requires a short-lived gregale:data-
    api:events workload assertion from a live instance of an active app; account and app come from it,
    never from the body. Each event publishes as an app-scoped producer-key event under the app's own
    source with key data-api.change.ID, so a republished change deduplicates while its receipt is
    retained. Events publish in order; publication stops at the first event that may succeed later, and
    later events are reported as retryable without being attempted. Owner API keys and application JWTs
    are not accepted.

    Args:
        body (DataAPIChangeEventBatch): A runtime's private publication of recorded changes, in
            recorded order.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DataAPIChangeEventBatchResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient,
    body: DataAPIChangeEventBatch,
) -> DataAPIChangeEventBatchResponse | Problem | None:
    """Publish recorded Data API row changes

     Private runtime surface for change hooks (ADR-20261022121642). Requires a short-lived gregale:data-
    api:events workload assertion from a live instance of an active app; account and app come from it,
    never from the body. Each event publishes as an app-scoped producer-key event under the app's own
    source with key data-api.change.ID, so a republished change deduplicates while its receipt is
    retained. Events publish in order; publication stops at the first event that may succeed later, and
    later events are reported as retryable without being attempted. Owner API keys and application JWTs
    are not accepted.

    Args:
        body (DataAPIChangeEventBatch): A runtime's private publication of recorded changes, in
            recorded order.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DataAPIChangeEventBatchResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
