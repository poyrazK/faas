from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.custom_metric_request import CustomMetricRequest
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    name: str,
    *,
    body: CustomMetricRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/apps/{slug}/custom-metrics/{name}".format(
            slug=quote(str(slug), safe=""),
            name=quote(str(name), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Any | Problem | None:
    if response.status_code == 204:
        response_204 = cast(Any, None)
        return response_204

    if response.status_code == 402:
        response_402 = Problem.from_dict(response.json())

        return response_402

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Response[Any | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient | Client,
    body: CustomMetricRequest,
) -> Response[Any | Problem]:
    """Push a custom application metric (ADR-202)

     Upserts one customer-pushed gauge, used as a scaling signal by a `metric: custom` target. The caller
    is frequently NOT the app — a cron, a database trigger, or the customer's own infrastructure — which
    is the point: a parked app has no process, so a scale-to-zero platform whose custom signal required
    a running instance could never scale from zero on it. The value is FLEET-TOTAL; the scheduler
    computes ceil(value / target). Pushing a name the app already holds always succeeds (it is an
    upsert); only a NEW name can hit the per-app cap.

    Args:
        slug (str):
        name (str):
        body (CustomMetricRequest): ADR-202 push body. The metric name travels in the URL path, so
            the body carries only the number that varies — which is what makes the push idempotent by
            construction.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        name=name,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient | Client,
    body: CustomMetricRequest,
) -> Any | Problem | None:
    """Push a custom application metric (ADR-202)

     Upserts one customer-pushed gauge, used as a scaling signal by a `metric: custom` target. The caller
    is frequently NOT the app — a cron, a database trigger, or the customer's own infrastructure — which
    is the point: a parked app has no process, so a scale-to-zero platform whose custom signal required
    a running instance could never scale from zero on it. The value is FLEET-TOTAL; the scheduler
    computes ceil(value / target). Pushing a name the app already holds always succeeds (it is an
    upsert); only a NEW name can hit the per-app cap.

    Args:
        slug (str):
        name (str):
        body (CustomMetricRequest): ADR-202 push body. The metric name travels in the URL path, so
            the body carries only the number that varies — which is what makes the push idempotent by
            construction.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return sync_detailed(
        slug=slug,
        name=name,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient | Client,
    body: CustomMetricRequest,
) -> Response[Any | Problem]:
    """Push a custom application metric (ADR-202)

     Upserts one customer-pushed gauge, used as a scaling signal by a `metric: custom` target. The caller
    is frequently NOT the app — a cron, a database trigger, or the customer's own infrastructure — which
    is the point: a parked app has no process, so a scale-to-zero platform whose custom signal required
    a running instance could never scale from zero on it. The value is FLEET-TOTAL; the scheduler
    computes ceil(value / target). Pushing a name the app already holds always succeeds (it is an
    upsert); only a NEW name can hit the per-app cap.

    Args:
        slug (str):
        name (str):
        body (CustomMetricRequest): ADR-202 push body. The metric name travels in the URL path, so
            the body carries only the number that varies — which is what makes the push idempotent by
            construction.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        name=name,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient | Client,
    body: CustomMetricRequest,
) -> Any | Problem | None:
    """Push a custom application metric (ADR-202)

     Upserts one customer-pushed gauge, used as a scaling signal by a `metric: custom` target. The caller
    is frequently NOT the app — a cron, a database trigger, or the customer's own infrastructure — which
    is the point: a parked app has no process, so a scale-to-zero platform whose custom signal required
    a running instance could never scale from zero on it. The value is FLEET-TOTAL; the scheduler
    computes ceil(value / target). Pushing a name the app already holds always succeeds (it is an
    upsert); only a NEW name can hit the per-app cap.

    Args:
        slug (str):
        name (str):
        body (CustomMetricRequest): ADR-202 push body. The metric name travels in the URL path, so
            the body carries only the number that varies — which is what makes the push idempotent by
            construction.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            name=name,
            client=client,
            body=body,
        )
    ).parsed
