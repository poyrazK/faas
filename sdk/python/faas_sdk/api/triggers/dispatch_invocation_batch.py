from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.dispatch_invocation_batch_body import DispatchInvocationBatchBody
from ...models.dispatch_invocation_batch_response_200 import DispatchInvocationBatchResponse200
from ...types import Response


def _get_kwargs(
    *,
    body: DispatchInvocationBatchBody,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/invocations:dispatch_batch",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> DispatchInvocationBatchResponse200 | None:
    if response.status_code == 200:
        response_200 = DispatchInvocationBatchResponse200.from_dict(response.json())

        return response_200

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[DispatchInvocationBatchResponse200]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: DispatchInvocationBatchBody,
) -> Response[DispatchInvocationBatchResponse200]:
    r"""Internal — schedd posts a batch envelope to the gateway.

     Internal-only route. Schedd invokes this once per closed
    batch. The gateway delivers records sequentially and returns one
    result per item. Each function response may include
    `{\"batchItemFailures\":[{\"itemIdentifier\":\"...\"}]}`.
    Durable queue records include an invocation ID and current claim
    attempt; source must be esm for these records.

    Args:
        body (DispatchInvocationBatchBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DispatchInvocationBatchResponse200]
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
    client: AuthenticatedClient | Client,
    body: DispatchInvocationBatchBody,
) -> DispatchInvocationBatchResponse200 | None:
    r"""Internal — schedd posts a batch envelope to the gateway.

     Internal-only route. Schedd invokes this once per closed
    batch. The gateway delivers records sequentially and returns one
    result per item. Each function response may include
    `{\"batchItemFailures\":[{\"itemIdentifier\":\"...\"}]}`.
    Durable queue records include an invocation ID and current claim
    attempt; source must be esm for these records.

    Args:
        body (DispatchInvocationBatchBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DispatchInvocationBatchResponse200
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: DispatchInvocationBatchBody,
) -> Response[DispatchInvocationBatchResponse200]:
    r"""Internal — schedd posts a batch envelope to the gateway.

     Internal-only route. Schedd invokes this once per closed
    batch. The gateway delivers records sequentially and returns one
    result per item. Each function response may include
    `{\"batchItemFailures\":[{\"itemIdentifier\":\"...\"}]}`.
    Durable queue records include an invocation ID and current claim
    attempt; source must be esm for these records.

    Args:
        body (DispatchInvocationBatchBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DispatchInvocationBatchResponse200]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: DispatchInvocationBatchBody,
) -> DispatchInvocationBatchResponse200 | None:
    r"""Internal — schedd posts a batch envelope to the gateway.

     Internal-only route. Schedd invokes this once per closed
    batch. The gateway delivers records sequentially and returns one
    result per item. Each function response may include
    `{\"batchItemFailures\":[{\"itemIdentifier\":\"...\"}]}`.
    Durable queue records include an invocation ID and current claim
    attempt; source must be esm for these records.

    Args:
        body (DispatchInvocationBatchBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DispatchInvocationBatchResponse200
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
