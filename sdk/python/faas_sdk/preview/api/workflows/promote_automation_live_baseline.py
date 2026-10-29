from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.automation_live_baseline import AutomationLiveBaseline
from ...models.problem import Problem
from ...models.promote_automation_live_baseline_request import PromoteAutomationLiveBaselineRequest
from ...types import Response


def _get_kwargs(
    slug: str,
    name: str,
    baseline: str,
    promotion_id: UUID,
    *,
    body: PromoteAutomationLiveBaselineRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/apps/{slug}/automations/{name}/live-baselines/{baseline}/promotions/{promotion_id}".format(
            slug=quote(str(slug), safe=""),
            name=quote(str(name), safe=""),
            baseline=quote(str(baseline), safe=""),
            promotion_id=quote(str(promotion_id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AutomationLiveBaseline | Problem | None:
    if response.status_code == 200:
        response_200 = AutomationLiveBaseline.from_dict(response.json())

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

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 410:
        response_410 = Problem.from_dict(response.json())

        return response_410

    if response.status_code == 413:
        response_413 = Problem.from_dict(response.json())

        return response_413

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
) -> Response[AutomationLiveBaseline | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    name: str,
    baseline: str,
    promotion_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: PromoteAutomationLiveBaselineRequest,
) -> Response[AutomationLiveBaseline | Problem]:
    """Deliberately promote passing retained evidence to a named baseline.

     Requires deploy write scope. Atomically checks the explicit current target and version 2 required-
    case/evidence-age policy, compares expected_revision, appends an attributed receipt and changes the
    head. expected_revision 0 creates. An identical retained promotion ID replays its original receipt
    without moving the head or rechecking. Never executes or checks assertions. At most 20 active and
    100 lifetime names per automation, with 100 receipts per name; active current suite groups are
    pinned within the existing suite limit, while linked run retention still applies. Retired names
    cannot be reused; original promotion retries return historical receipts without reactivation.

    Args:
        slug (str):
        name (str):
        baseline (str):
        promotion_id (UUID):
        body (PromoteAutomationLiveBaselineRequest): Explicit target suite, reviewed policy and
            expected baseline revision for an audited promotion.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AutomationLiveBaseline | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        name=name,
        baseline=baseline,
        promotion_id=promotion_id,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    name: str,
    baseline: str,
    promotion_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: PromoteAutomationLiveBaselineRequest,
) -> AutomationLiveBaseline | Problem | None:
    """Deliberately promote passing retained evidence to a named baseline.

     Requires deploy write scope. Atomically checks the explicit current target and version 2 required-
    case/evidence-age policy, compares expected_revision, appends an attributed receipt and changes the
    head. expected_revision 0 creates. An identical retained promotion ID replays its original receipt
    without moving the head or rechecking. Never executes or checks assertions. At most 20 active and
    100 lifetime names per automation, with 100 receipts per name; active current suite groups are
    pinned within the existing suite limit, while linked run retention still applies. Retired names
    cannot be reused; original promotion retries return historical receipts without reactivation.

    Args:
        slug (str):
        name (str):
        baseline (str):
        promotion_id (UUID):
        body (PromoteAutomationLiveBaselineRequest): Explicit target suite, reviewed policy and
            expected baseline revision for an audited promotion.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AutomationLiveBaseline | Problem
    """

    return sync_detailed(
        slug=slug,
        name=name,
        baseline=baseline,
        promotion_id=promotion_id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    name: str,
    baseline: str,
    promotion_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: PromoteAutomationLiveBaselineRequest,
) -> Response[AutomationLiveBaseline | Problem]:
    """Deliberately promote passing retained evidence to a named baseline.

     Requires deploy write scope. Atomically checks the explicit current target and version 2 required-
    case/evidence-age policy, compares expected_revision, appends an attributed receipt and changes the
    head. expected_revision 0 creates. An identical retained promotion ID replays its original receipt
    without moving the head or rechecking. Never executes or checks assertions. At most 20 active and
    100 lifetime names per automation, with 100 receipts per name; active current suite groups are
    pinned within the existing suite limit, while linked run retention still applies. Retired names
    cannot be reused; original promotion retries return historical receipts without reactivation.

    Args:
        slug (str):
        name (str):
        baseline (str):
        promotion_id (UUID):
        body (PromoteAutomationLiveBaselineRequest): Explicit target suite, reviewed policy and
            expected baseline revision for an audited promotion.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AutomationLiveBaseline | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        name=name,
        baseline=baseline,
        promotion_id=promotion_id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    name: str,
    baseline: str,
    promotion_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: PromoteAutomationLiveBaselineRequest,
) -> AutomationLiveBaseline | Problem | None:
    """Deliberately promote passing retained evidence to a named baseline.

     Requires deploy write scope. Atomically checks the explicit current target and version 2 required-
    case/evidence-age policy, compares expected_revision, appends an attributed receipt and changes the
    head. expected_revision 0 creates. An identical retained promotion ID replays its original receipt
    without moving the head or rechecking. Never executes or checks assertions. At most 20 active and
    100 lifetime names per automation, with 100 receipts per name; active current suite groups are
    pinned within the existing suite limit, while linked run retention still applies. Retired names
    cannot be reused; original promotion retries return historical receipts without reactivation.

    Args:
        slug (str):
        name (str):
        baseline (str):
        promotion_id (UUID):
        body (PromoteAutomationLiveBaselineRequest): Explicit target suite, reviewed policy and
            expected baseline revision for an audited promotion.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AutomationLiveBaseline | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            name=name,
            baseline=baseline,
            promotion_id=promotion_id,
            client=client,
            body=body,
        )
    ).parsed
