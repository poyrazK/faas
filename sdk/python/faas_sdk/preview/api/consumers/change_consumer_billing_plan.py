from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.change_consumer_billing_plan_request import ChangeConsumerBillingPlanRequest
from ...models.consumer_billing_plan_change_response import ConsumerBillingPlanChangeResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: ChangeConsumerBillingPlanRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/consumer-billing/{slug}/plan".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ConsumerBillingPlanChangeResponse | Problem | None:
    if response.status_code == 200:
        response_200 = ConsumerBillingPlanChangeResponse.from_dict(response.json())

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

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ConsumerBillingPlanChangeResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    *,
    client: AuthenticatedClient,
    body: ChangeConsumerBillingPlanRequest,
) -> Response[ConsumerBillingPlanChangeResponse | Problem]:
    """Switch to another offered plan from the next minute with your consumer key.

     Only when the app owner lets customers change plans. The app owner
    gets a consumer.plan_changed webhook.

    Args:
        slug (str):
        body (ChangeConsumerBillingPlanRequest): Switches to one of the offered plans from the
            next minute.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ConsumerBillingPlanChangeResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient,
    body: ChangeConsumerBillingPlanRequest,
) -> ConsumerBillingPlanChangeResponse | Problem | None:
    """Switch to another offered plan from the next minute with your consumer key.

     Only when the app owner lets customers change plans. The app owner
    gets a consumer.plan_changed webhook.

    Args:
        slug (str):
        body (ChangeConsumerBillingPlanRequest): Switches to one of the offered plans from the
            next minute.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ConsumerBillingPlanChangeResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient,
    body: ChangeConsumerBillingPlanRequest,
) -> Response[ConsumerBillingPlanChangeResponse | Problem]:
    """Switch to another offered plan from the next minute with your consumer key.

     Only when the app owner lets customers change plans. The app owner
    gets a consumer.plan_changed webhook.

    Args:
        slug (str):
        body (ChangeConsumerBillingPlanRequest): Switches to one of the offered plans from the
            next minute.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ConsumerBillingPlanChangeResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient,
    body: ChangeConsumerBillingPlanRequest,
) -> ConsumerBillingPlanChangeResponse | Problem | None:
    """Switch to another offered plan from the next minute with your consumer key.

     Only when the app owner lets customers change plans. The app owner
    gets a consumer.plan_changed webhook.

    Args:
        slug (str):
        body (ChangeConsumerBillingPlanRequest): Switches to one of the offered plans from the
            next minute.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ConsumerBillingPlanChangeResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
