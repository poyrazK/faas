from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.get_route_health_investigation_customer_group_by import (
    GetRouteHealthInvestigationCustomerGroupBy,
)
from ...models.get_route_health_investigation_method import (
    GetRouteHealthInvestigationMethod,
)
from ...models.get_route_health_investigation_status_code import (
    GetRouteHealthInvestigationStatusCode,
)
from ...models.problem import Problem
from ...models.route_health_investigation import RouteHealthInvestigation
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    deployment: UUID,
    *,
    method: GetRouteHealthInvestigationMethod,
    path: str,
    status_code: GetRouteHealthInvestigationStatusCode | Unset = 0,
    customer_group_by: GetRouteHealthInvestigationCustomerGroupBy | Unset = UNSET,
    customer_id: UUID | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_method: str = method
    params["method"] = json_method

    params["path"] = path

    json_status_code: int | Unset = UNSET
    if not isinstance(status_code, Unset):
        json_status_code = status_code

    params["status_code"] = json_status_code

    json_customer_group_by: str | Unset = UNSET
    if not isinstance(customer_group_by, Unset):
        json_customer_group_by = customer_group_by

    params["customer_group_by"] = json_customer_group_by

    json_customer_id: str | Unset = UNSET
    if not isinstance(customer_id, Unset):
        json_customer_id = str(customer_id)
    params["customer_id"] = json_customer_id

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/route-health/deployments/{deployment}/investigation".format(
            slug=quote(str(slug), safe=""),
            deployment=quote(str(deployment), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | RouteHealthInvestigation | None:
    if response.status_code == 200:
        response_200 = RouteHealthInvestigation.from_dict(response.json())

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
) -> Response[Problem | RouteHealthInvestigation]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    deployment: UUID,
    *,
    client: AuthenticatedClient | Client,
    method: GetRouteHealthInvestigationMethod,
    path: str,
    status_code: GetRouteHealthInvestigationStatusCode | Unset = 0,
    customer_group_by: GetRouteHealthInvestigationCustomerGroupBy | Unset = UNSET,
    customer_id: UUID | Unset = UNSET,
) -> Response[Problem | RouteHealthInvestigation]:
    """Find retained request examples for a configured route health signal.

     Requires app read access, completed MFA and request telemetry entitlement. Aggregate report,
    selected finding and bounded examples share one read-only repeatable-read snapshot, exact
    candidate/stable pair and closed health windows. Status code zero selects all 5xx; nonzero codes
    must be watched on this route. Optional customer selection uses recorded tenant or consumer
    attribution, including identities outside the customer report cap and revoked consumers. Customer
    UUID input explicitly includes the selected ID; other customer identities are excluded. Counts
    preserve publisher weights; examples are telemetry rows and may represent multiple requests. Trace
    links do not guarantee retained spans. This diagnostic read changes no rollout state and includes no
    payloads, credentials, request headers or raw URLs.

    Args:
        slug (str):
        deployment (UUID):
        method (GetRouteHealthInvestigationMethod):
        path (str):
        status_code (GetRouteHealthInvestigationStatusCode | Unset):  Default: 0.
        customer_group_by (GetRouteHealthInvestigationCustomerGroupBy | Unset):
        customer_id (UUID | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RouteHealthInvestigation]
    """

    kwargs = _get_kwargs(
        slug=slug,
        deployment=deployment,
        method=method,
        path=path,
        status_code=status_code,
        customer_group_by=customer_group_by,
        customer_id=customer_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    deployment: UUID,
    *,
    client: AuthenticatedClient | Client,
    method: GetRouteHealthInvestigationMethod,
    path: str,
    status_code: GetRouteHealthInvestigationStatusCode | Unset = 0,
    customer_group_by: GetRouteHealthInvestigationCustomerGroupBy | Unset = UNSET,
    customer_id: UUID | Unset = UNSET,
) -> Problem | RouteHealthInvestigation | None:
    """Find retained request examples for a configured route health signal.

     Requires app read access, completed MFA and request telemetry entitlement. Aggregate report,
    selected finding and bounded examples share one read-only repeatable-read snapshot, exact
    candidate/stable pair and closed health windows. Status code zero selects all 5xx; nonzero codes
    must be watched on this route. Optional customer selection uses recorded tenant or consumer
    attribution, including identities outside the customer report cap and revoked consumers. Customer
    UUID input explicitly includes the selected ID; other customer identities are excluded. Counts
    preserve publisher weights; examples are telemetry rows and may represent multiple requests. Trace
    links do not guarantee retained spans. This diagnostic read changes no rollout state and includes no
    payloads, credentials, request headers or raw URLs.

    Args:
        slug (str):
        deployment (UUID):
        method (GetRouteHealthInvestigationMethod):
        path (str):
        status_code (GetRouteHealthInvestigationStatusCode | Unset):  Default: 0.
        customer_group_by (GetRouteHealthInvestigationCustomerGroupBy | Unset):
        customer_id (UUID | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RouteHealthInvestigation
    """

    return sync_detailed(
        slug=slug,
        deployment=deployment,
        client=client,
        method=method,
        path=path,
        status_code=status_code,
        customer_group_by=customer_group_by,
        customer_id=customer_id,
    ).parsed


async def asyncio_detailed(
    slug: str,
    deployment: UUID,
    *,
    client: AuthenticatedClient | Client,
    method: GetRouteHealthInvestigationMethod,
    path: str,
    status_code: GetRouteHealthInvestigationStatusCode | Unset = 0,
    customer_group_by: GetRouteHealthInvestigationCustomerGroupBy | Unset = UNSET,
    customer_id: UUID | Unset = UNSET,
) -> Response[Problem | RouteHealthInvestigation]:
    """Find retained request examples for a configured route health signal.

     Requires app read access, completed MFA and request telemetry entitlement. Aggregate report,
    selected finding and bounded examples share one read-only repeatable-read snapshot, exact
    candidate/stable pair and closed health windows. Status code zero selects all 5xx; nonzero codes
    must be watched on this route. Optional customer selection uses recorded tenant or consumer
    attribution, including identities outside the customer report cap and revoked consumers. Customer
    UUID input explicitly includes the selected ID; other customer identities are excluded. Counts
    preserve publisher weights; examples are telemetry rows and may represent multiple requests. Trace
    links do not guarantee retained spans. This diagnostic read changes no rollout state and includes no
    payloads, credentials, request headers or raw URLs.

    Args:
        slug (str):
        deployment (UUID):
        method (GetRouteHealthInvestigationMethod):
        path (str):
        status_code (GetRouteHealthInvestigationStatusCode | Unset):  Default: 0.
        customer_group_by (GetRouteHealthInvestigationCustomerGroupBy | Unset):
        customer_id (UUID | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RouteHealthInvestigation]
    """

    kwargs = _get_kwargs(
        slug=slug,
        deployment=deployment,
        method=method,
        path=path,
        status_code=status_code,
        customer_group_by=customer_group_by,
        customer_id=customer_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    deployment: UUID,
    *,
    client: AuthenticatedClient | Client,
    method: GetRouteHealthInvestigationMethod,
    path: str,
    status_code: GetRouteHealthInvestigationStatusCode | Unset = 0,
    customer_group_by: GetRouteHealthInvestigationCustomerGroupBy | Unset = UNSET,
    customer_id: UUID | Unset = UNSET,
) -> Problem | RouteHealthInvestigation | None:
    """Find retained request examples for a configured route health signal.

     Requires app read access, completed MFA and request telemetry entitlement. Aggregate report,
    selected finding and bounded examples share one read-only repeatable-read snapshot, exact
    candidate/stable pair and closed health windows. Status code zero selects all 5xx; nonzero codes
    must be watched on this route. Optional customer selection uses recorded tenant or consumer
    attribution, including identities outside the customer report cap and revoked consumers. Customer
    UUID input explicitly includes the selected ID; other customer identities are excluded. Counts
    preserve publisher weights; examples are telemetry rows and may represent multiple requests. Trace
    links do not guarantee retained spans. This diagnostic read changes no rollout state and includes no
    payloads, credentials, request headers or raw URLs.

    Args:
        slug (str):
        deployment (UUID):
        method (GetRouteHealthInvestigationMethod):
        path (str):
        status_code (GetRouteHealthInvestigationStatusCode | Unset):  Default: 0.
        customer_group_by (GetRouteHealthInvestigationCustomerGroupBy | Unset):
        customer_id (UUID | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RouteHealthInvestigation
    """

    return (
        await asyncio_detailed(
            slug=slug,
            deployment=deployment,
            client=client,
            method=method,
            path=path,
            status_code=status_code,
            customer_group_by=customer_group_by,
            customer_id=customer_id,
        )
    ).parsed
