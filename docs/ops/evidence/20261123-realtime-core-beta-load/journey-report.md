# Gregale journey report

- target: https://api.gregale.dev
- tier: full
- cli: gregale v0.1.18-rc.373
- started: 2026-11-23T08:18:34Z

**11 passed, 0 failed, 0 known bugs, 0 skipped**

## Since the last run

since target: https://api.gregale.dev:
- now passing: TestRealtimeLoad/delete_endpoint
- flaky over the last 5 runs: TestRealtimeLoad/connect_all, TestRealtimeLoad/delete_endpoint

| Step | Result | Seconds | Note |
|---|---|---|---|
| `TestRealtimeLoad/realtime_enabled` | PASS | 0.8 |  |
| `TestRealtimeLoad/deploy_issuer_app` | PASS | 29.0 |  |
| `TestRealtimeLoad/create_endpoint` | PASS | 0.8 |  |
| `TestRealtimeLoad/connect_all` | PASS | 13.3 |  |
| `TestRealtimeLoad/hold_idle` | PASS | 90.6 |  |
| `TestRealtimeLoad/principal_fan_out` | PASS | 11.9 |  |
| `TestRealtimeLoad/subscribe_channel_group` | PASS | 2.8 |  |
| `TestRealtimeLoad/channel_fan_out` | PASS | 11.8 |  |
| `TestRealtimeLoad/all_still_connected` | PASS | 0.0 |  |
| `TestRealtimeLoad/write_evidence` | PASS | 0.0 |  |
| `TestRealtimeLoad/delete_endpoint` | PASS | 2.9 |  |
