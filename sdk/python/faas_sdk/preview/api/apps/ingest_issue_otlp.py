from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.ingest_issue_otlp_body import IngestIssueOTLPBody
from ...models.ingest_issue_otlp_response_200 import IngestIssueOTLPResponse200
from ...models.ingest_issue_otlp_signal import IngestIssueOTLPSignal
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    signal: IngestIssueOTLPSignal,
    *,
    body: IngestIssueOTLPBody,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/issue-events/otlp/{signal}".format(
            slug=quote(str(slug), safe=""),
            signal=quote(str(signal), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> IngestIssueOTLPResponse200 | Problem | None:
    if response.status_code == 200:
        response_200 = IngestIssueOTLPResponse200.from_dict(response.json())

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

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

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
) -> Response[IngestIssueOTLPResponse200 | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    signal: IngestIssueOTLPSignal,
    *,
    client: AuthenticatedClient,
    body: IngestIssueOTLPBody,
) -> Response[IngestIssueOTLPResponse200 | Problem]:
    """Ingest OTLP JSON exception events

     Accepts bounded OTLP trace exception events or exception log records using a deployment-bound issue
    token. Unrelated telemetry is ignored. Deterministic event IDs permit safe retry after partial
    delivery. Protobuf encoding is not supported.

    Args:
        slug (str):
        signal (IngestIssueOTLPSignal):
        body (IngestIssueOTLPBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[IngestIssueOTLPResponse200 | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        signal=signal,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    signal: IngestIssueOTLPSignal,
    *,
    client: AuthenticatedClient,
    body: IngestIssueOTLPBody,
) -> IngestIssueOTLPResponse200 | Problem | None:
    """Ingest OTLP JSON exception events

     Accepts bounded OTLP trace exception events or exception log records using a deployment-bound issue
    token. Unrelated telemetry is ignored. Deterministic event IDs permit safe retry after partial
    delivery. Protobuf encoding is not supported.

    Args:
        slug (str):
        signal (IngestIssueOTLPSignal):
        body (IngestIssueOTLPBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        IngestIssueOTLPResponse200 | Problem
    """

    return sync_detailed(
        slug=slug,
        signal=signal,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    signal: IngestIssueOTLPSignal,
    *,
    client: AuthenticatedClient,
    body: IngestIssueOTLPBody,
) -> Response[IngestIssueOTLPResponse200 | Problem]:
    """Ingest OTLP JSON exception events

     Accepts bounded OTLP trace exception events or exception log records using a deployment-bound issue
    token. Unrelated telemetry is ignored. Deterministic event IDs permit safe retry after partial
    delivery. Protobuf encoding is not supported.

    Args:
        slug (str):
        signal (IngestIssueOTLPSignal):
        body (IngestIssueOTLPBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[IngestIssueOTLPResponse200 | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        signal=signal,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    signal: IngestIssueOTLPSignal,
    *,
    client: AuthenticatedClient,
    body: IngestIssueOTLPBody,
) -> IngestIssueOTLPResponse200 | Problem | None:
    """Ingest OTLP JSON exception events

     Accepts bounded OTLP trace exception events or exception log records using a deployment-bound issue
    token. Unrelated telemetry is ignored. Deterministic event IDs permit safe retry after partial
    delivery. Protobuf encoding is not supported.

    Args:
        slug (str):
        signal (IngestIssueOTLPSignal):
        body (IngestIssueOTLPBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        IngestIssueOTLPResponse200 | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            signal=signal,
            client=client,
            body=body,
        )
    ).parsed
