from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.create_trigger_batch_request import CreateTriggerBatchRequest
from ...models.create_trigger_batch_response import CreateTriggerBatchResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    body: CreateTriggerBatchRequest,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/triggers:batch_create",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> CreateTriggerBatchResponse | Problem | None:
    if response.status_code == 200:
        response_200 = CreateTriggerBatchResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 402:
        response_402 = Problem.from_dict(response.json())

        return response_402

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[CreateTriggerBatchResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: CreateTriggerBatchRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[CreateTriggerBatchResponse | Problem]:
    """Bulk-create triggers from a gregale.yaml fragment.

     Fires a `triggers:` and `event_triggers:` fragment at the server,
    validates via the same path the CLI uses, and returns per-row ids
    and any per-row RFC 7807 codes. Event triggers for the target app
    are applied the way a source deploy applies them:
    existing subscriptions are kept, and a failure is one `errors`
    row after its changes are rolled back.

    Args:
        idempotency_key (str | Unset):
        body (CreateTriggerBatchRequest): Inline-manifest path (POST /v1/triggers:batch_create) —
            fire a
            gregale.yaml blob at the server without staging a tarball.
            The handler re-uses validateManifestBytes from the manifest
            apply path.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[CreateTriggerBatchResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    body: CreateTriggerBatchRequest,
    idempotency_key: str | Unset = UNSET,
) -> CreateTriggerBatchResponse | Problem | None:
    """Bulk-create triggers from a gregale.yaml fragment.

     Fires a `triggers:` and `event_triggers:` fragment at the server,
    validates via the same path the CLI uses, and returns per-row ids
    and any per-row RFC 7807 codes. Event triggers for the target app
    are applied the way a source deploy applies them:
    existing subscriptions are kept, and a failure is one `errors`
    row after its changes are rolled back.

    Args:
        idempotency_key (str | Unset):
        body (CreateTriggerBatchRequest): Inline-manifest path (POST /v1/triggers:batch_create) —
            fire a
            gregale.yaml blob at the server without staging a tarball.
            The handler re-uses validateManifestBytes from the manifest
            apply path.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        CreateTriggerBatchResponse | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: CreateTriggerBatchRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[CreateTriggerBatchResponse | Problem]:
    """Bulk-create triggers from a gregale.yaml fragment.

     Fires a `triggers:` and `event_triggers:` fragment at the server,
    validates via the same path the CLI uses, and returns per-row ids
    and any per-row RFC 7807 codes. Event triggers for the target app
    are applied the way a source deploy applies them:
    existing subscriptions are kept, and a failure is one `errors`
    row after its changes are rolled back.

    Args:
        idempotency_key (str | Unset):
        body (CreateTriggerBatchRequest): Inline-manifest path (POST /v1/triggers:batch_create) —
            fire a
            gregale.yaml blob at the server without staging a tarball.
            The handler re-uses validateManifestBytes from the manifest
            apply path.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[CreateTriggerBatchResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: CreateTriggerBatchRequest,
    idempotency_key: str | Unset = UNSET,
) -> CreateTriggerBatchResponse | Problem | None:
    """Bulk-create triggers from a gregale.yaml fragment.

     Fires a `triggers:` and `event_triggers:` fragment at the server,
    validates via the same path the CLI uses, and returns per-row ids
    and any per-row RFC 7807 codes. Event triggers for the target app
    are applied the way a source deploy applies them:
    existing subscriptions are kept, and a failure is one `errors`
    row after its changes are rolled back.

    Args:
        idempotency_key (str | Unset):
        body (CreateTriggerBatchRequest): Inline-manifest path (POST /v1/triggers:batch_create) —
            fire a
            gregale.yaml blob at the server without staging a tarball.
            The handler re-uses validateManifestBytes from the manifest
            apply path.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        CreateTriggerBatchResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
