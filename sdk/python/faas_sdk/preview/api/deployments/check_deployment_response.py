from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.deployment_response_check_request import DeploymentResponseCheckRequest
from ...models.deployment_response_check_result import DeploymentResponseCheckResult
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    deployment: str,
    *,
    body: DeploymentResponseCheckRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/deployments/{deployment}/response-check".format(
            slug=quote(str(slug), safe=""),
            deployment=quote(str(deployment), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> DeploymentResponseCheckResult | Problem | None:
    if response.status_code == 200:
        response_200 = DeploymentResponseCheckResult.from_dict(response.json())

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
) -> Response[DeploymentResponseCheckResult | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    deployment: str,
    *,
    client: AuthenticatedClient | Client,
    body: DeploymentResponseCheckRequest,
) -> Response[DeploymentResponseCheckResult | Problem]:
    """Check one GET against an owned deployment.

     Requires deployment-write access and MFA. Pins a live deployment, app UUID
    and runtime scope; named scopes also require project and environment UUIDs.
    Gatewayd may wake that exact deployment through existing scheduler admission.
    Sends one GET without forwarding API credentials, cookies, arbitrary headers
    or a request body. Does not follow redirects or use public response caches.
    Returns the actual application HTTP status, selected identities and a bounded
    UTF-8 response preview. A 200 API envelope includes application failures; only
    the inner HTTP status describes the workload response. This is owner access,
    not proof of public routing or customer authentication. Application-owned
    authentication is preserved. Rechecks target identity after forwarding.
    Timeout is 20 seconds, path limit 2048 bytes, preview limit 4096 bytes.
    Response content and path queries are ephemeral. Responses are not cached.

    Args:
        slug (str):
        deployment (str):
        body (DeploymentResponseCheckRequest): Explicit identity and one GET path; named scopes
            require project_id and environment_id.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DeploymentResponseCheckResult | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        deployment=deployment,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    deployment: str,
    *,
    client: AuthenticatedClient | Client,
    body: DeploymentResponseCheckRequest,
) -> DeploymentResponseCheckResult | Problem | None:
    """Check one GET against an owned deployment.

     Requires deployment-write access and MFA. Pins a live deployment, app UUID
    and runtime scope; named scopes also require project and environment UUIDs.
    Gatewayd may wake that exact deployment through existing scheduler admission.
    Sends one GET without forwarding API credentials, cookies, arbitrary headers
    or a request body. Does not follow redirects or use public response caches.
    Returns the actual application HTTP status, selected identities and a bounded
    UTF-8 response preview. A 200 API envelope includes application failures; only
    the inner HTTP status describes the workload response. This is owner access,
    not proof of public routing or customer authentication. Application-owned
    authentication is preserved. Rechecks target identity after forwarding.
    Timeout is 20 seconds, path limit 2048 bytes, preview limit 4096 bytes.
    Response content and path queries are ephemeral. Responses are not cached.

    Args:
        slug (str):
        deployment (str):
        body (DeploymentResponseCheckRequest): Explicit identity and one GET path; named scopes
            require project_id and environment_id.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DeploymentResponseCheckResult | Problem
    """

    return sync_detailed(
        slug=slug,
        deployment=deployment,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    deployment: str,
    *,
    client: AuthenticatedClient | Client,
    body: DeploymentResponseCheckRequest,
) -> Response[DeploymentResponseCheckResult | Problem]:
    """Check one GET against an owned deployment.

     Requires deployment-write access and MFA. Pins a live deployment, app UUID
    and runtime scope; named scopes also require project and environment UUIDs.
    Gatewayd may wake that exact deployment through existing scheduler admission.
    Sends one GET without forwarding API credentials, cookies, arbitrary headers
    or a request body. Does not follow redirects or use public response caches.
    Returns the actual application HTTP status, selected identities and a bounded
    UTF-8 response preview. A 200 API envelope includes application failures; only
    the inner HTTP status describes the workload response. This is owner access,
    not proof of public routing or customer authentication. Application-owned
    authentication is preserved. Rechecks target identity after forwarding.
    Timeout is 20 seconds, path limit 2048 bytes, preview limit 4096 bytes.
    Response content and path queries are ephemeral. Responses are not cached.

    Args:
        slug (str):
        deployment (str):
        body (DeploymentResponseCheckRequest): Explicit identity and one GET path; named scopes
            require project_id and environment_id.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DeploymentResponseCheckResult | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        deployment=deployment,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    deployment: str,
    *,
    client: AuthenticatedClient | Client,
    body: DeploymentResponseCheckRequest,
) -> DeploymentResponseCheckResult | Problem | None:
    """Check one GET against an owned deployment.

     Requires deployment-write access and MFA. Pins a live deployment, app UUID
    and runtime scope; named scopes also require project and environment UUIDs.
    Gatewayd may wake that exact deployment through existing scheduler admission.
    Sends one GET without forwarding API credentials, cookies, arbitrary headers
    or a request body. Does not follow redirects or use public response caches.
    Returns the actual application HTTP status, selected identities and a bounded
    UTF-8 response preview. A 200 API envelope includes application failures; only
    the inner HTTP status describes the workload response. This is owner access,
    not proof of public routing or customer authentication. Application-owned
    authentication is preserved. Rechecks target identity after forwarding.
    Timeout is 20 seconds, path limit 2048 bytes, preview limit 4096 bytes.
    Response content and path queries are ephemeral. Responses are not cached.

    Args:
        slug (str):
        deployment (str):
        body (DeploymentResponseCheckRequest): Explicit identity and one GET path; named scopes
            require project_id and environment_id.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DeploymentResponseCheckResult | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            deployment=deployment,
            client=client,
            body=body,
        )
    ).parsed
