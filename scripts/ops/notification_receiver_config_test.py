#!/usr/bin/env python3
"""Receiver intent must agree with the deployed template without reading secrets."""

import json
from pathlib import Path
import re
import unittest

import jinja2
import yaml

ROOT = Path(__file__).resolve().parents[2]
ROLES = ROOT / "deploy/ansible/roles"


class ReceiverConfigTest(unittest.TestCase):
    def test_intent_matches_every_configured_transport_group(self):
        defaults = yaml.safe_load((ROLES / "alertmanager/defaults/main.yml").read_text())
        env = jinja2.Environment(undefined=jinja2.StrictUndefined)
        env.filters.update(to_json=json.dumps, bool=bool)
        config_template = env.from_string((ROLES / "alertmanager/templates/alertmanager.yml.j2").read_text())
        intent_template = env.from_string((ROLES / "notification_receiver_metrics/templates/notification-receiver-inventory.prom.j2").read_text())
        cases = [
            {},
            {"am_slack_webhook_url_file": "/unread/slack"},
            {"am_discord_webhook_url_file": "/unread/discord"},
            {"am_slack_warn_webhook_url_file": "/unread/warn"},
            {"am_smtp_smarthost": "smtp.example:587", "am_smtp_password_file": "/unread/smtp"},
            {"am_pushover_token_file": "/unread/token", "am_pushover_user_key_file": "/unread/user"},
            {"am_pushover_token_file": "/unread/token"},
            {"am_smtp_smarthost": "smtp.example:587"},
            {"am_slack_webhook_url_file": "  ", "am_slack_warn_webhook_url_file": "  "},
            {"am_heartbeat_url_file": " /unread/heartbeat "},
            {"am_slack_webhook_url_file": "/unread/slack", "am_discord_webhook_url_file": "/unread/discord",
             "am_slack_warn_webhook_url_file": "/unread/warn", "am_heartbeat_url_file": "/unread/heartbeat",
             "am_smtp_smarthost": "smtp.example:587", "am_smtp_password_file": "/unread/smtp",
             "am_pushover_token_file": "/unread/token", "am_pushover_user_key_file": "/unread/user"},
        ]
        for settings in cases:
            with self.subTest(settings=settings):
                values = dict(defaults, **settings)
                config = yaml.safe_load(config_template.render(**values))
                want = {(receiver["name"], key.removesuffix("_configs"))
                        for receiver in config["receivers"] for key, value in receiver.items()
                        if key.endswith("_configs") and value}
                intent = intent_template.render(**values)
                pairs = re.findall(r'^faas_notification_receiver_expected\{receiver="([a-z-]+)",integration="([a-z]+)"\} 1$', intent, re.M)
                self.assertEqual(set(pairs), want)
                self.assertEqual(len(pairs), len(want), "one group per receiver/integration, even with two chat endpoints")
                self.assertNotIn("/unread/", intent)
                self.assertNotIn("smtp.example", intent)


if __name__ == "__main__":
    unittest.main()
