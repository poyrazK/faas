from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.event_schema_rollout_request import EventSchemaRolloutRequest
from ...models.event_schema_rollout_response import EventSchemaRolloutResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    *,
    body: EventSchemaRolloutRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/event-schemas:preview-rollout",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> EventSchemaRolloutResponse | Problem | None:
    if response.status_code == 200:
        response_200 = EventSchemaRolloutResponse.from_dict(response.json())

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

    if response.status_code == 504:
        response_504 = Problem.from_dict(response.json())

        return response_504

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[EventSchemaRolloutResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient,
    body: EventSchemaRolloutRequest,
) -> Response[EventSchemaRolloutResponse | Problem]:
    """Preview schema version consumer coverage and payload validation.

     Read-only. Requires apps:read or admin. Supply a proposed Draft 2020-12 schema, or omit schema to
    check a registered version. Reports enabled application consumers whose source/type patterns match;
    version acceptance does not evaluate content filters or guarantee delivery. Optional retained checks
    scan the newest 1000 account receipts within the acceptance-time range, validate at most 100
    matching payloads and at most 4 MiB, and never certify schema compatibility or complete history. No
    schema is registered and no events or deliveries are created.

    Args:
        body (EventSchemaRolloutRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventSchemaRolloutResponse | Problem]
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
    body: EventSchemaRolloutRequest,
) -> EventSchemaRolloutResponse | Problem | None:
    """Preview schema version consumer coverage and payload validation.

     Read-only. Requires apps:read or admin. Supply a proposed Draft 2020-12 schema, or omit schema to
    check a registered version. Reports enabled application consumers whose source/type patterns match;
    version acceptance does not evaluate content filters or guarantee delivery. Optional retained checks
    scan the newest 1000 account receipts within the acceptance-time range, validate at most 100
    matching payloads and at most 4 MiB, and never certify schema compatibility or complete history. No
    schema is registered and no events or deliveries are created.

    Args:
        body (EventSchemaRolloutRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventSchemaRolloutResponse | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient,
    body: EventSchemaRolloutRequest,
) -> Response[EventSchemaRolloutResponse | Problem]:
    """Preview schema version consumer coverage and payload validation.

     Read-only. Requires apps:read or admin. Supply a proposed Draft 2020-12 schema, or omit schema to
    check a registered version. Reports enabled application consumers whose source/type patterns match;
    version acceptance does not evaluate content filters or guarantee delivery. Optional retained checks
    scan the newest 1000 account receipts within the acceptance-time range, validate at most 100
    matching payloads and at most 4 MiB, and never certify schema compatibility or complete history. No
    schema is registered and no events or deliveries are created.

    Args:
        body (EventSchemaRolloutRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventSchemaRolloutResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient,
    body: EventSchemaRolloutRequest,
) -> EventSchemaRolloutResponse | Problem | None:
    """Preview schema version consumer coverage and payload validation.

     Read-only. Requires apps:read or admin. Supply a proposed Draft 2020-12 schema, or omit schema to
    check a registered version. Reports enabled application consumers whose source/type patterns match;
    version acceptance does not evaluate content filters or guarantee delivery. Optional retained checks
    scan the newest 1000 account receipts within the acceptance-time range, validate at most 100
    matching payloads and at most 4 MiB, and never certify schema compatibility or complete history. No
    schema is registered and no events or deliveries are created.

    Args:
        body (EventSchemaRolloutRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventSchemaRolloutResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
