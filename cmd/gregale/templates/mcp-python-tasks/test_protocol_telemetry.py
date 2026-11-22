import http.client
import json
import time
import unittest
from email.message import Message
from urllib.request import Request, urlopen
from urllib.error import HTTPError
import test_server
from protocol_telemetry import ProtocolObservation
from auth import Authenticator


class ProtocolTelemetryTests(unittest.TestCase):
    setUp = test_server.MCPStarterTests.setUp
    tearDown = test_server.MCPStarterTests.tearDown

    def test_wire_outcomes_privacy_and_correlation(self):
        events=[]
        self.http.protocol_telemetry_sink=events.append
        self.config["auth"]={"mode":"open"}
        self.http.authenticator=Authenticator(self.config)
        for method,params,outcome,tool in [
            ("tools/call",{"name":"greet","arguments":{"name":"sensitive-payload"}},"success","greet"),
            ("tools/call",{"name":"add","arguments":{"a":"sensitive-payload","b":1}},"tool_error","add"),
            ("resources/read",{"uri":"missing://private-resource"},"rpc_error","none"),
            ("private-method-token",{},"rpc_error","none"),
            ("tools/call",{"name":"private-tool-token","arguments":{}},"tool_error","other"),
        ]:
            events.clear()
            headers={"Content-Type":"application/json","X-Faas-Request-Id":"a1234567-1234-4234-8234-123456789abc","traceparent":"00-123456789abcdef0123456789abcdef0-123456789abcdef0-01","Authorization":"Bearer do-not-log-this"}
            body={"jsonrpc":"2.0","id":"private-rpc-id","method":method,"params":params}
            try:response=urlopen(Request(self.base+"/mcp",data=json.dumps(body).encode(),headers=headers),timeout=3)
            except HTTPError as error:response=error
            with response:response.read()
            for _ in range(100):
                if events:break
                time.sleep(.01)
            self.assertEqual(len(events),1)
            event=events[0]
            self.assertEqual(event["method"],"other" if method=="private-method-token" else method)
            self.assertEqual(event["tool"],tool);self.assertEqual(event["outcome"],outcome)
            self.assertGreaterEqual(event["execution_duration_ms"],0)
            self.assertEqual(event["trace_id"],"123456789abcdef0123456789abcdef0")
            self.assertEqual(event["request_id"],"a1234567-1234-4234-8234-123456789abc")
            for secret in ["sensitive-payload","private-rpc-id","private-method-token","private-tool-token","do-not-log-this","missing://private-resource"]:self.assertNotIn(secret,json.dumps(event))

    def test_stream_disconnect(self):
        events=[];self.http.protocol_telemetry_sink=events.append
        connection=http.client.HTTPConnection("127.0.0.1",self.http.server_address[1],timeout=3)
        connection.request("POST","/mcp",body=json.dumps({"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"stream_demo","arguments":{},"_meta":{"progressToken":"private-progress"}}}),headers={"Content-Type":"application/json","Accept":"text/event-stream"})
        response=connection.getresponse();response.read(1);response.close();connection.close()
        for _ in range(200):
            if events:break
            time.sleep(.01)
        self.assertEqual(len(events),1);self.assertEqual(events[0]["outcome"],"cancelled");self.assertGreaterEqual(events[0]["stream_duration_ms"],0)

    def test_timeout_and_invalid_ids(self):
        headers=Message();headers["X-Faas-Request-Id"]="invalid-secret";headers["traceparent"]="00-"+"0"*32+"-123456789abcdef0-01"
        events=[];observation=ProtocolObservation({"method":"tools/call","params":{"name":"private-tool"}},headers,events.append)
        observation.failed("timeout");observation.finish()
        self.assertEqual(events[0]["outcome"],"timeout");self.assertNotIn("request_id",events[0]);self.assertNotIn("trace_id",events[0]);self.assertNotIn("execution_duration_ms",events[0])

class HeartbeatTests(unittest.TestCase):
    def test_heartbeat_without_requests(self):
        import contextlib
        import io
        from protocol_telemetry import start_protocol_heartbeat
        output = io.StringIO()
        with contextlib.redirect_stdout(output):
            stop = start_protocol_heartbeat()
            stop()
            stop()
        event = json.loads(output.getvalue())
        self.assertEqual(event["event"], "mcp_telemetry_heartbeat")
        self.assertEqual(event["version"], 1)
        self.assertEqual(set(event), {"event", "version", "loss_alert"})
