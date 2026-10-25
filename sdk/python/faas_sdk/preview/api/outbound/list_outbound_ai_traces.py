import datetime
from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.outbound_ai_trace_list import OutboundAITraceList
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    integration: UUID,
    *,
    limit: int | Unset = UNSET,
    before: datetime.datetime | Unset = UNSET,
    app_id: UUID | Unset = UNSET,
    end_user: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["limit"] = limit

    json_before: str | Unset = UNSET
    if not isinstance(before, Unset):
        json_before = before.isoformat()
    params["before"] = json_before

    json_app_id: str | Unset = UNSET
    if not isinstance(app_id, Unset):
        json_app_id = str(app_id)
    params["app_id"] = json_app_id

    params["end_user"] = end_user

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/outbound/integrations/{integration}/ai-traces".format(
            integration=quote(str(integration), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> OutboundAITraceList | Problem | None:
    if response.status_code == 200:
        response_200 = OutboundAITraceList.from_dict(response.json())

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

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[OutboundAITraceList | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    integration: UUID,
    *,
    client: AuthenticatedClient | Client,
    limit: int | Unset = UNSET,
    before: datetime.datetime | Unset = UNSET,
    app_id: UUID | Unset = UNSET,
    end_user: str | Unset = UNSET,
) -> Response[OutboundAITraceList | Problem]:
    """Page an integration's AI traces.

     Requires MFA and read scope (ADR 20261020202035). A trace groups the calls of one agent task, named
    by X-Gregale-AI-Trace or the trace ID of a W3C traceparent header. Traces are recorded while the
    integration keeps a request log or has trace limits, and are kept for 7 days. Newest last call
    first.

    Args:
        integration (UUID):
        limit (int | Unset):
        before (datetime.datetime | Unset):
        app_id (UUID | Unset):
        end_user (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OutboundAITraceList | Problem]
    """

    kwargs = _get_kwargs(
        integration=integration,
        limit=limit,
        before=before,
        app_id=app_id,
        end_user=end_user,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    integration: UUID,
    *,
    client: AuthenticatedClient | Client,
    limit: int | Unset = UNSET,
    before: datetime.datetime | Unset = UNSET,
    app_id: UUID | Unset = UNSET,
    end_user: str | Unset = UNSET,
) -> OutboundAITraceList | Problem | None:
    """Page an integration's AI traces.

     Requires MFA and read scope (ADR 20261020202035). A trace groups the calls of one agent task, named
    by X-Gregale-AI-Trace or the trace ID of a W3C traceparent header. Traces are recorded while the
    integration keeps a request log or has trace limits, and are kept for 7 days. Newest last call
    first.

    Args:
        integration (UUID):
        limit (int | Unset):
        before (datetime.datetime | Unset):
        app_id (UUID | Unset):
        end_user (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OutboundAITraceList | Problem
    """

    return sync_detailed(
        integration=integration,
        client=client,
        limit=limit,
        before=before,
        app_id=app_id,
        end_user=end_user,
    ).parsed


async def asyncio_detailed(
    integration: UUID,
    *,
    client: AuthenticatedClient | Client,
    limit: int | Unset = UNSET,
    before: datetime.datetime | Unset = UNSET,
    app_id: UUID | Unset = UNSET,
    end_user: str | Unset = UNSET,
) -> Response[OutboundAITraceList | Problem]:
    """Page an integration's AI traces.

     Requires MFA and read scope (ADR 20261020202035). A trace groups the calls of one agent task, named
    by X-Gregale-AI-Trace or the trace ID of a W3C traceparent header. Traces are recorded while the
    integration keeps a request log or has trace limits, and are kept for 7 days. Newest last call
    first.

    Args:
        integration (UUID):
        limit (int | Unset):
        before (datetime.datetime | Unset):
        app_id (UUID | Unset):
        end_user (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OutboundAITraceList | Problem]
    """

    kwargs = _get_kwargs(
        integration=integration,
        limit=limit,
        before=before,
        app_id=app_id,
        end_user=end_user,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    integration: UUID,
    *,
    client: AuthenticatedClient | Client,
    limit: int | Unset = UNSET,
    before: datetime.datetime | Unset = UNSET,
    app_id: UUID | Unset = UNSET,
    end_user: str | Unset = UNSET,
) -> OutboundAITraceList | Problem | None:
    """Page an integration's AI traces.

     Requires MFA and read scope (ADR 20261020202035). A trace groups the calls of one agent task, named
    by X-Gregale-AI-Trace or the trace ID of a W3C traceparent header. Traces are recorded while the
    integration keeps a request log or has trace limits, and are kept for 7 days. Newest last call
    first.

    Args:
        integration (UUID):
        limit (int | Unset):
        before (datetime.datetime | Unset):
        app_id (UUID | Unset):
        end_user (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OutboundAITraceList | Problem
    """

    return (
        await asyncio_detailed(
            integration=integration,
            client=client,
            limit=limit,
            before=before,
            app_id=app_id,
            end_user=end_user,
        )
    ).parsed
