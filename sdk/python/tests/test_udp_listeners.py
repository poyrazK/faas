from faas_sdk.api.apps import create_app_udp_listener, update_app_udp_listener
from faas_sdk.models.create_udp_listener_request import CreateUDPListenerRequest
from faas_sdk.models.update_udp_listener_request import UpdateUDPListenerRequest


def test_udp_allocation_encoding():
    omitted = CreateUDPListenerRequest(name="dns", guest_port=5353)
    assert create_app_udp_listener._get_kwargs("app", body=omitted)["json"] == {"name": "dns", "guest_port": 5353}
    explicit = CreateUDPListenerRequest(name="dns", guest_port=5353, public_port=0)
    wire = create_app_udp_listener._get_kwargs("app", body=explicit)["json"]
    assert wire == {"name": "dns", "guest_port": 5353, "public_port": 0}
    assert CreateUDPListenerRequest.from_dict(wire).public_port == 0


def test_udp_disable_encoding():
    args = update_app_udp_listener._get_kwargs("app", "dns", body=UpdateUDPListenerRequest(enabled=False))
    assert args["method"] == "patch"
    assert args["url"] == "/v1/apps/app/udp-listeners/dns"
    assert args["json"] == {"enabled": False}
