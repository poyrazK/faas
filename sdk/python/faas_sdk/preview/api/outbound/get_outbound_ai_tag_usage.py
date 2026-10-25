import datetime
from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.outbound_ai_tag_usage_response import OutboundAITagUsageResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    integration: UUID,
    *,
    date: datetime.date | Unset = UNSET,
    key: str | Unset = UNSET,
    limit: int | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_date: str | Unset = UNSET
    if not isinstance(date, Unset):
        json_date = date.isoformat()
    params["date"] = json_date

    params["key"] = key

    params["limit"] = limit

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/outbound/integrations/{integration}/ai-usage/tags".format(
            integration=quote(str(integration), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> OutboundAITagUsageResponse | Problem | None:
    if response.status_code == 200:
        response_200 = OutboundAITagUsageResponse.from_dict(response.json())

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
) -> Response[OutboundAITagUsageResponse | Problem]:
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
    date: datetime.date | Unset = UNSET,
    key: str | Unset = UNSET,
    limit: int | Unset = UNSET,
) -> Response[OutboundAITagUsageResponse | Problem]:
    """Read one UTC day of AI gateway usage by tag.

     Requires MFA and read scope (ADR-1436). Tags are the key=value pairs apps send in X-Gregale-AI-Tags;
    rows are kept for 31 days and listed by cost, then tokens, with each tag's daily cost limit.

    Args:
        integration (UUID):
        date (datetime.date | Unset):
        key (str | Unset):
        limit (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OutboundAITagUsageResponse | Problem]
    """

    kwargs = _get_kwargs(
        integration=integration,
        date=date,
        key=key,
        limit=limit,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    integration: UUID,
    *,
    client: AuthenticatedClient | Client,
    date: datetime.date | Unset = UNSET,
    key: str | Unset = UNSET,
    limit: int | Unset = UNSET,
) -> OutboundAITagUsageResponse | Problem | None:
    """Read one UTC day of AI gateway usage by tag.

     Requires MFA and read scope (ADR-1436). Tags are the key=value pairs apps send in X-Gregale-AI-Tags;
    rows are kept for 31 days and listed by cost, then tokens, with each tag's daily cost limit.

    Args:
        integration (UUID):
        date (datetime.date | Unset):
        key (str | Unset):
        limit (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OutboundAITagUsageResponse | Problem
    """

    return sync_detailed(
        integration=integration,
        client=client,
        date=date,
        key=key,
        limit=limit,
    ).parsed


async def asyncio_detailed(
    integration: UUID,
    *,
    client: AuthenticatedClient | Client,
    date: datetime.date | Unset = UNSET,
    key: str | Unset = UNSET,
    limit: int | Unset = UNSET,
) -> Response[OutboundAITagUsageResponse | Problem]:
    """Read one UTC day of AI gateway usage by tag.

     Requires MFA and read scope (ADR-1436). Tags are the key=value pairs apps send in X-Gregale-AI-Tags;
    rows are kept for 31 days and listed by cost, then tokens, with each tag's daily cost limit.

    Args:
        integration (UUID):
        date (datetime.date | Unset):
        key (str | Unset):
        limit (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OutboundAITagUsageResponse | Problem]
    """

    kwargs = _get_kwargs(
        integration=integration,
        date=date,
        key=key,
        limit=limit,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    integration: UUID,
    *,
    client: AuthenticatedClient | Client,
    date: datetime.date | Unset = UNSET,
    key: str | Unset = UNSET,
    limit: int | Unset = UNSET,
) -> OutboundAITagUsageResponse | Problem | None:
    """Read one UTC day of AI gateway usage by tag.

     Requires MFA and read scope (ADR-1436). Tags are the key=value pairs apps send in X-Gregale-AI-Tags;
    rows are kept for 31 days and listed by cost, then tokens, with each tag's daily cost limit.

    Args:
        integration (UUID):
        date (datetime.date | Unset):
        key (str | Unset):
        limit (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OutboundAITagUsageResponse | Problem
    """

    return (
        await asyncio_detailed(
            integration=integration,
            client=client,
            date=date,
            key=key,
            limit=limit,
        )
    ).parsed
