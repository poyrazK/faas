from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.preview_route_monitor_request import PreviewRouteMonitorRequest
from ...models.problem import Problem
from ...models.route_monitor_preview import RouteMonitorPreview
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    body: PreviewRouteMonitorRequest,
    customer_details: bool | Unset = False,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    params: dict[str, Any] = {}

    params["customer_details"] = customer_details

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/route-monitor/preview".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | RouteMonitorPreview | None:
    if response.status_code == 200:
        response_200 = RouteMonitorPreview.from_dict(response.json())

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

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | RouteMonitorPreview]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: PreviewRouteMonitorRequest,
    customer_details: bool | Unset = False,
) -> Response[Problem | RouteMonitorPreview]:
    """Evaluate proposed route budgets against recent production traffic without saving them.

     Read-only evaluation of proposed absolute budgets against the two latest closed UTC minute windows
    for the sole fully serving default-scope deployment. Requires app read access and completed MFA.
    Uses the deployment and rollout observation anchors, but not the saved monitor update anchor,
    because the proposal has not been saved. If the proposal groups by customer, customer IDs are
    redacted by default. Saving changed configuration resets its observation anchor, so this preview is
    current evidence and not a prediction of the first post-save report. Violated and unknown findings
    return 200; no configuration, incident or traffic state is changed. Body limit is 16 KiB.

    Args:
        slug (str):
        customer_details (bool | Unset):  Default: False.
        body (PreviewRouteMonitorRequest): Proposed route monitor intent evaluated against recent
            production observations without being saved.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RouteMonitorPreview]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
        customer_details=customer_details,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: PreviewRouteMonitorRequest,
    customer_details: bool | Unset = False,
) -> Problem | RouteMonitorPreview | None:
    """Evaluate proposed route budgets against recent production traffic without saving them.

     Read-only evaluation of proposed absolute budgets against the two latest closed UTC minute windows
    for the sole fully serving default-scope deployment. Requires app read access and completed MFA.
    Uses the deployment and rollout observation anchors, but not the saved monitor update anchor,
    because the proposal has not been saved. If the proposal groups by customer, customer IDs are
    redacted by default. Saving changed configuration resets its observation anchor, so this preview is
    current evidence and not a prediction of the first post-save report. Violated and unknown findings
    return 200; no configuration, incident or traffic state is changed. Body limit is 16 KiB.

    Args:
        slug (str):
        customer_details (bool | Unset):  Default: False.
        body (PreviewRouteMonitorRequest): Proposed route monitor intent evaluated against recent
            production observations without being saved.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RouteMonitorPreview
    """

    return sync_detailed(
        slug=slug,
        client=client,
        body=body,
        customer_details=customer_details,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: PreviewRouteMonitorRequest,
    customer_details: bool | Unset = False,
) -> Response[Problem | RouteMonitorPreview]:
    """Evaluate proposed route budgets against recent production traffic without saving them.

     Read-only evaluation of proposed absolute budgets against the two latest closed UTC minute windows
    for the sole fully serving default-scope deployment. Requires app read access and completed MFA.
    Uses the deployment and rollout observation anchors, but not the saved monitor update anchor,
    because the proposal has not been saved. If the proposal groups by customer, customer IDs are
    redacted by default. Saving changed configuration resets its observation anchor, so this preview is
    current evidence and not a prediction of the first post-save report. Violated and unknown findings
    return 200; no configuration, incident or traffic state is changed. Body limit is 16 KiB.

    Args:
        slug (str):
        customer_details (bool | Unset):  Default: False.
        body (PreviewRouteMonitorRequest): Proposed route monitor intent evaluated against recent
            production observations without being saved.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RouteMonitorPreview]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
        customer_details=customer_details,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: PreviewRouteMonitorRequest,
    customer_details: bool | Unset = False,
) -> Problem | RouteMonitorPreview | None:
    """Evaluate proposed route budgets against recent production traffic without saving them.

     Read-only evaluation of proposed absolute budgets against the two latest closed UTC minute windows
    for the sole fully serving default-scope deployment. Requires app read access and completed MFA.
    Uses the deployment and rollout observation anchors, but not the saved monitor update anchor,
    because the proposal has not been saved. If the proposal groups by customer, customer IDs are
    redacted by default. Saving changed configuration resets its observation anchor, so this preview is
    current evidence and not a prediction of the first post-save report. Violated and unknown findings
    return 200; no configuration, incident or traffic state is changed. Body limit is 16 KiB.

    Args:
        slug (str):
        customer_details (bool | Unset):  Default: False.
        body (PreviewRouteMonitorRequest): Proposed route monitor intent evaluated against recent
            production observations without being saved.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RouteMonitorPreview
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
            customer_details=customer_details,
        )
    ).parsed
