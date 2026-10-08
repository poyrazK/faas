from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.approve_route_lifecycle_request import ApproveRouteLifecycleRequest
from ...models.problem import Problem
from ...models.route_lifecycle_approval import RouteLifecycleApproval
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: ApproveRouteLifecycleRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/route-lifecycle/approvals".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | RouteLifecycleApproval | None:
    if response.status_code == 201:
        response_201 = RouteLifecycleApproval.from_dict(response.json())

        return response_201

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

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | RouteLifecycleApproval]:
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
    body: ApproveRouteLifecycleRequest,
) -> Response[Problem | RouteLifecycleApproval]:
    """Approve exact captured lifecycle successor changes.

     Requires account admin authorization, owner or organization owner/admin identity, completed MFA and
    same-origin session protection. The server validates explicit authorized canonical or verified
    custom-domain HTTPS successor mappings against captured inline rooted OpenAPI operation contracts.
    Unknown or incompatible results fail closed. Receipt bindings include gate, saved requirements and
    removal policy revisions, configured policy fingerprint and authoritative capture hashes. Receipts
    expire after one hour and bind destination ownership, capture, hostname and production routing.
    Destination capture, domain, rule and routing changes invalidate receipts. All destination pins must
    be supplied together; project destinations require one active production graph member and verified
    frozen workload settings; ambiguous routing is unavailable for approval. Only successor-review
    findings on production traffic increases may be cleared; other lifecycle, removal and contract
    checks remain enforced. Workers also require the receipt database configuration binding to remain
    current.

    Args:
        slug (str):
        body (ApproveRouteLifecycleRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RouteLifecycleApproval]
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
    client: AuthenticatedClient | Client,
    body: ApproveRouteLifecycleRequest,
) -> Problem | RouteLifecycleApproval | None:
    """Approve exact captured lifecycle successor changes.

     Requires account admin authorization, owner or organization owner/admin identity, completed MFA and
    same-origin session protection. The server validates explicit authorized canonical or verified
    custom-domain HTTPS successor mappings against captured inline rooted OpenAPI operation contracts.
    Unknown or incompatible results fail closed. Receipt bindings include gate, saved requirements and
    removal policy revisions, configured policy fingerprint and authoritative capture hashes. Receipts
    expire after one hour and bind destination ownership, capture, hostname and production routing.
    Destination capture, domain, rule and routing changes invalidate receipts. All destination pins must
    be supplied together; project destinations require one active production graph member and verified
    frozen workload settings; ambiguous routing is unavailable for approval. Only successor-review
    findings on production traffic increases may be cleared; other lifecycle, removal and contract
    checks remain enforced. Workers also require the receipt database configuration binding to remain
    current.

    Args:
        slug (str):
        body (ApproveRouteLifecycleRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RouteLifecycleApproval
    """

    return sync_detailed(
        slug=slug,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: ApproveRouteLifecycleRequest,
) -> Response[Problem | RouteLifecycleApproval]:
    """Approve exact captured lifecycle successor changes.

     Requires account admin authorization, owner or organization owner/admin identity, completed MFA and
    same-origin session protection. The server validates explicit authorized canonical or verified
    custom-domain HTTPS successor mappings against captured inline rooted OpenAPI operation contracts.
    Unknown or incompatible results fail closed. Receipt bindings include gate, saved requirements and
    removal policy revisions, configured policy fingerprint and authoritative capture hashes. Receipts
    expire after one hour and bind destination ownership, capture, hostname and production routing.
    Destination capture, domain, rule and routing changes invalidate receipts. All destination pins must
    be supplied together; project destinations require one active production graph member and verified
    frozen workload settings; ambiguous routing is unavailable for approval. Only successor-review
    findings on production traffic increases may be cleared; other lifecycle, removal and contract
    checks remain enforced. Workers also require the receipt database configuration binding to remain
    current.

    Args:
        slug (str):
        body (ApproveRouteLifecycleRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RouteLifecycleApproval]
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
    client: AuthenticatedClient | Client,
    body: ApproveRouteLifecycleRequest,
) -> Problem | RouteLifecycleApproval | None:
    """Approve exact captured lifecycle successor changes.

     Requires account admin authorization, owner or organization owner/admin identity, completed MFA and
    same-origin session protection. The server validates explicit authorized canonical or verified
    custom-domain HTTPS successor mappings against captured inline rooted OpenAPI operation contracts.
    Unknown or incompatible results fail closed. Receipt bindings include gate, saved requirements and
    removal policy revisions, configured policy fingerprint and authoritative capture hashes. Receipts
    expire after one hour and bind destination ownership, capture, hostname and production routing.
    Destination capture, domain, rule and routing changes invalidate receipts. All destination pins must
    be supplied together; project destinations require one active production graph member and verified
    frozen workload settings; ambiguous routing is unavailable for approval. Only successor-review
    findings on production traffic increases may be cleared; other lifecycle, removal and contract
    checks remain enforced. Workers also require the receipt database configuration binding to remain
    current.

    Args:
        slug (str):
        body (ApproveRouteLifecycleRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RouteLifecycleApproval
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
