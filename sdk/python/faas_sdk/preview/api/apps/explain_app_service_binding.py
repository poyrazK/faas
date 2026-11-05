from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.service_binding_explanation import ServiceBindingExplanation
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    service: str,
    *,
    deployment_id: UUID,
    scope: str | Unset = UNSET,
    method: str | Unset = "GET",
    path: str | Unset = "/",
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_deployment_id = str(deployment_id)
    params["deployment_id"] = json_deployment_id

    params["scope"] = scope

    params["method"] = method

    params["path"] = path

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/bindings/services/{service}/explanation".format(
            slug=quote(str(slug), safe=""),
            service=quote(str(service), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | ServiceBindingExplanation | None:
    if response.status_code == 200:
        response_200 = ServiceBindingExplanation.from_dict(response.json())

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

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

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
) -> Response[Problem | ServiceBindingExplanation]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    service: str,
    *,
    client: AuthenticatedClient | Client,
    deployment_id: UUID,
    scope: str | Unset = UNSET,
    method: str | Unset = "GET",
    path: str | Unset = "/",
) -> Response[Problem | ServiceBindingExplanation]:
    """Explain service authorization for an exact caller deployment.

     Read-only policy and routing observation using the gateway app authorizer,
    managed environment binding resolver and project release resolver. Requires
    apps:read or admin and completed MFA. The deployment must belong to the
    selected app. Its environment is inferred; scope, when supplied, asserts
    that environment. Managed deployments never fall back to global app names.
    Target identities are returned only within the selected account. Policy
    denials and incomplete contexts return 200 with result denied or
    indeterminate. Missing or foreign caller deployments return 404. Previews,
    qualification guests and development bridges are not evaluated. No tasks,
    guest requests, writes or provider calls occur. Separate metadata reads
    are not an atomic snapshot, caller authentication, runtime adoption or
    connectivity evidence. Unpinned routes do not select a target deployment.
    Responses use Cache-Control: no-store.

    Args:
        slug (str):
        service (str):
        deployment_id (UUID):
        scope (str | Unset):
        method (str | Unset):  Default: 'GET'.
        path (str | Unset):  Default: '/'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ServiceBindingExplanation]
    """

    kwargs = _get_kwargs(
        slug=slug,
        service=service,
        deployment_id=deployment_id,
        scope=scope,
        method=method,
        path=path,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    service: str,
    *,
    client: AuthenticatedClient | Client,
    deployment_id: UUID,
    scope: str | Unset = UNSET,
    method: str | Unset = "GET",
    path: str | Unset = "/",
) -> Problem | ServiceBindingExplanation | None:
    """Explain service authorization for an exact caller deployment.

     Read-only policy and routing observation using the gateway app authorizer,
    managed environment binding resolver and project release resolver. Requires
    apps:read or admin and completed MFA. The deployment must belong to the
    selected app. Its environment is inferred; scope, when supplied, asserts
    that environment. Managed deployments never fall back to global app names.
    Target identities are returned only within the selected account. Policy
    denials and incomplete contexts return 200 with result denied or
    indeterminate. Missing or foreign caller deployments return 404. Previews,
    qualification guests and development bridges are not evaluated. No tasks,
    guest requests, writes or provider calls occur. Separate metadata reads
    are not an atomic snapshot, caller authentication, runtime adoption or
    connectivity evidence. Unpinned routes do not select a target deployment.
    Responses use Cache-Control: no-store.

    Args:
        slug (str):
        service (str):
        deployment_id (UUID):
        scope (str | Unset):
        method (str | Unset):  Default: 'GET'.
        path (str | Unset):  Default: '/'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ServiceBindingExplanation
    """

    return sync_detailed(
        slug=slug,
        service=service,
        client=client,
        deployment_id=deployment_id,
        scope=scope,
        method=method,
        path=path,
    ).parsed


async def asyncio_detailed(
    slug: str,
    service: str,
    *,
    client: AuthenticatedClient | Client,
    deployment_id: UUID,
    scope: str | Unset = UNSET,
    method: str | Unset = "GET",
    path: str | Unset = "/",
) -> Response[Problem | ServiceBindingExplanation]:
    """Explain service authorization for an exact caller deployment.

     Read-only policy and routing observation using the gateway app authorizer,
    managed environment binding resolver and project release resolver. Requires
    apps:read or admin and completed MFA. The deployment must belong to the
    selected app. Its environment is inferred; scope, when supplied, asserts
    that environment. Managed deployments never fall back to global app names.
    Target identities are returned only within the selected account. Policy
    denials and incomplete contexts return 200 with result denied or
    indeterminate. Missing or foreign caller deployments return 404. Previews,
    qualification guests and development bridges are not evaluated. No tasks,
    guest requests, writes or provider calls occur. Separate metadata reads
    are not an atomic snapshot, caller authentication, runtime adoption or
    connectivity evidence. Unpinned routes do not select a target deployment.
    Responses use Cache-Control: no-store.

    Args:
        slug (str):
        service (str):
        deployment_id (UUID):
        scope (str | Unset):
        method (str | Unset):  Default: 'GET'.
        path (str | Unset):  Default: '/'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ServiceBindingExplanation]
    """

    kwargs = _get_kwargs(
        slug=slug,
        service=service,
        deployment_id=deployment_id,
        scope=scope,
        method=method,
        path=path,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    service: str,
    *,
    client: AuthenticatedClient | Client,
    deployment_id: UUID,
    scope: str | Unset = UNSET,
    method: str | Unset = "GET",
    path: str | Unset = "/",
) -> Problem | ServiceBindingExplanation | None:
    """Explain service authorization for an exact caller deployment.

     Read-only policy and routing observation using the gateway app authorizer,
    managed environment binding resolver and project release resolver. Requires
    apps:read or admin and completed MFA. The deployment must belong to the
    selected app. Its environment is inferred; scope, when supplied, asserts
    that environment. Managed deployments never fall back to global app names.
    Target identities are returned only within the selected account. Policy
    denials and incomplete contexts return 200 with result denied or
    indeterminate. Missing or foreign caller deployments return 404. Previews,
    qualification guests and development bridges are not evaluated. No tasks,
    guest requests, writes or provider calls occur. Separate metadata reads
    are not an atomic snapshot, caller authentication, runtime adoption or
    connectivity evidence. Unpinned routes do not select a target deployment.
    Responses use Cache-Control: no-store.

    Args:
        slug (str):
        service (str):
        deployment_id (UUID):
        scope (str | Unset):
        method (str | Unset):  Default: 'GET'.
        path (str | Unset):  Default: '/'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ServiceBindingExplanation
    """

    return (
        await asyncio_detailed(
            slug=slug,
            service=service,
            client=client,
            deployment_id=deployment_id,
            scope=scope,
            method=method,
            path=path,
        )
    ).parsed
