import datetime
from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.export_outbound_ai_tag_usage_format import (
    ExportOutboundAITagUsageFormat,
)
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    integration: UUID,
    *,
    from_: datetime.date | Unset = UNSET,
    to: datetime.date | Unset = UNSET,
    key: str | Unset = UNSET,
    format_: ExportOutboundAITagUsageFormat | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_from_: str | Unset = UNSET
    if not isinstance(from_, Unset):
        json_from_ = from_.isoformat()
    params["from"] = json_from_

    json_to: str | Unset = UNSET
    if not isinstance(to, Unset):
        json_to = to.isoformat()
    params["to"] = json_to

    params["key"] = key

    json_format_: str | Unset = UNSET
    if not isinstance(format_, Unset):
        json_format_ = format_

    params["format"] = json_format_

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/outbound/integrations/{integration}/ai-usage/tags/export".format(
            integration=quote(str(integration), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Problem | str | None:
    if response.status_code == 200:
        response_200 = response.text
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


def _build_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Response[Problem | str]:
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
    from_: datetime.date | Unset = UNSET,
    to: datetime.date | Unset = UNSET,
    key: str | Unset = UNSET,
    format_: ExportOutboundAITagUsageFormat | Unset = UNSET,
) -> Response[Problem | str]:
    """Export a day range of AI gateway usage by tag.

     Requires MFA and read scope. Streams every tag row of the UTC days from through to, optionally of
    one key, ordered by day and tag, as NDJSON (the default) or CSV with the columns of
    OutboundAITagUsageExportRow.

    Args:
        integration (UUID):
        from_ (datetime.date | Unset):
        to (datetime.date | Unset):
        key (str | Unset):
        format_ (ExportOutboundAITagUsageFormat | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | str]
    """

    kwargs = _get_kwargs(
        integration=integration,
        from_=from_,
        to=to,
        key=key,
        format_=format_,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    integration: UUID,
    *,
    client: AuthenticatedClient | Client,
    from_: datetime.date | Unset = UNSET,
    to: datetime.date | Unset = UNSET,
    key: str | Unset = UNSET,
    format_: ExportOutboundAITagUsageFormat | Unset = UNSET,
) -> Problem | str | None:
    """Export a day range of AI gateway usage by tag.

     Requires MFA and read scope. Streams every tag row of the UTC days from through to, optionally of
    one key, ordered by day and tag, as NDJSON (the default) or CSV with the columns of
    OutboundAITagUsageExportRow.

    Args:
        integration (UUID):
        from_ (datetime.date | Unset):
        to (datetime.date | Unset):
        key (str | Unset):
        format_ (ExportOutboundAITagUsageFormat | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | str
    """

    return sync_detailed(
        integration=integration,
        client=client,
        from_=from_,
        to=to,
        key=key,
        format_=format_,
    ).parsed


async def asyncio_detailed(
    integration: UUID,
    *,
    client: AuthenticatedClient | Client,
    from_: datetime.date | Unset = UNSET,
    to: datetime.date | Unset = UNSET,
    key: str | Unset = UNSET,
    format_: ExportOutboundAITagUsageFormat | Unset = UNSET,
) -> Response[Problem | str]:
    """Export a day range of AI gateway usage by tag.

     Requires MFA and read scope. Streams every tag row of the UTC days from through to, optionally of
    one key, ordered by day and tag, as NDJSON (the default) or CSV with the columns of
    OutboundAITagUsageExportRow.

    Args:
        integration (UUID):
        from_ (datetime.date | Unset):
        to (datetime.date | Unset):
        key (str | Unset):
        format_ (ExportOutboundAITagUsageFormat | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | str]
    """

    kwargs = _get_kwargs(
        integration=integration,
        from_=from_,
        to=to,
        key=key,
        format_=format_,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    integration: UUID,
    *,
    client: AuthenticatedClient | Client,
    from_: datetime.date | Unset = UNSET,
    to: datetime.date | Unset = UNSET,
    key: str | Unset = UNSET,
    format_: ExportOutboundAITagUsageFormat | Unset = UNSET,
) -> Problem | str | None:
    """Export a day range of AI gateway usage by tag.

     Requires MFA and read scope. Streams every tag row of the UTC days from through to, optionally of
    one key, ordered by day and tag, as NDJSON (the default) or CSV with the columns of
    OutboundAITagUsageExportRow.

    Args:
        integration (UUID):
        from_ (datetime.date | Unset):
        to (datetime.date | Unset):
        key (str | Unset):
        format_ (ExportOutboundAITagUsageFormat | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | str
    """

    return (
        await asyncio_detailed(
            integration=integration,
            client=client,
            from_=from_,
            to=to,
            key=key,
            format_=format_,
        )
    ).parsed
