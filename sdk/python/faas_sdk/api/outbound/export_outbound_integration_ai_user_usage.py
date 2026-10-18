import datetime
from http import HTTPStatus
from io import BytesIO
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.export_outbound_integration_ai_user_usage_format import (
    ExportOutboundIntegrationAIUserUsageFormat,
)
from ...models.problem import Problem
from ...types import UNSET, File, Response, Unset


def _get_kwargs(
    integration: UUID,
    *,
    from_: datetime.date | Unset = UNSET,
    to: datetime.date | Unset = UNSET,
    format_: ExportOutboundIntegrationAIUserUsageFormat | Unset = "ndjson",
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

    json_format_: str | Unset = UNSET
    if not isinstance(format_, Unset):
        json_format_ = format_

    params["format"] = json_format_

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/outbound/integrations/{integration}/ai-usage/users/export".format(
            integration=quote(str(integration), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> File | Problem | None:
    if response.status_code == 200:
        response_200 = File(payload=BytesIO(response.content))

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


def _build_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Response[File | Problem]:
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
    format_: ExportOutboundIntegrationAIUserUsageFormat | Unset = "ndjson",
) -> Response[File | Problem]:
    """Export per-end-user AI gateway usage for a range of UTC days.

     Requires MFA and read scope (ADR-995). Streams every per-end-user row of the days from through to,
    ordered by day and end user. CSV columns are usage_date, end_user, requests, error_requests,
    input_tokens, output_tokens, tokens and cost_millicents; NDJSON lines carry the same fields.

    Args:
        integration (UUID):
        from_ (datetime.date | Unset):
        to (datetime.date | Unset):
        format_ (ExportOutboundIntegrationAIUserUsageFormat | Unset):  Default: 'ndjson'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[File | Problem]
    """

    kwargs = _get_kwargs(
        integration=integration,
        from_=from_,
        to=to,
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
    format_: ExportOutboundIntegrationAIUserUsageFormat | Unset = "ndjson",
) -> File | Problem | None:
    """Export per-end-user AI gateway usage for a range of UTC days.

     Requires MFA and read scope (ADR-995). Streams every per-end-user row of the days from through to,
    ordered by day and end user. CSV columns are usage_date, end_user, requests, error_requests,
    input_tokens, output_tokens, tokens and cost_millicents; NDJSON lines carry the same fields.

    Args:
        integration (UUID):
        from_ (datetime.date | Unset):
        to (datetime.date | Unset):
        format_ (ExportOutboundIntegrationAIUserUsageFormat | Unset):  Default: 'ndjson'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        File | Problem
    """

    return sync_detailed(
        integration=integration,
        client=client,
        from_=from_,
        to=to,
        format_=format_,
    ).parsed


async def asyncio_detailed(
    integration: UUID,
    *,
    client: AuthenticatedClient | Client,
    from_: datetime.date | Unset = UNSET,
    to: datetime.date | Unset = UNSET,
    format_: ExportOutboundIntegrationAIUserUsageFormat | Unset = "ndjson",
) -> Response[File | Problem]:
    """Export per-end-user AI gateway usage for a range of UTC days.

     Requires MFA and read scope (ADR-995). Streams every per-end-user row of the days from through to,
    ordered by day and end user. CSV columns are usage_date, end_user, requests, error_requests,
    input_tokens, output_tokens, tokens and cost_millicents; NDJSON lines carry the same fields.

    Args:
        integration (UUID):
        from_ (datetime.date | Unset):
        to (datetime.date | Unset):
        format_ (ExportOutboundIntegrationAIUserUsageFormat | Unset):  Default: 'ndjson'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[File | Problem]
    """

    kwargs = _get_kwargs(
        integration=integration,
        from_=from_,
        to=to,
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
    format_: ExportOutboundIntegrationAIUserUsageFormat | Unset = "ndjson",
) -> File | Problem | None:
    """Export per-end-user AI gateway usage for a range of UTC days.

     Requires MFA and read scope (ADR-995). Streams every per-end-user row of the days from through to,
    ordered by day and end user. CSV columns are usage_date, end_user, requests, error_requests,
    input_tokens, output_tokens, tokens and cost_millicents; NDJSON lines carry the same fields.

    Args:
        integration (UUID):
        from_ (datetime.date | Unset):
        to (datetime.date | Unset):
        format_ (ExportOutboundIntegrationAIUserUsageFormat | Unset):  Default: 'ndjson'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        File | Problem
    """

    return (
        await asyncio_detailed(
            integration=integration,
            client=client,
            from_=from_,
            to=to,
            format_=format_,
        )
    ).parsed
