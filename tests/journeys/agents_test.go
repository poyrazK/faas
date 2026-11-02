package journeys

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Inventory row 167: Gregale Agents, the stateless agent shape
// (ADR-20261030125501 step 4). The journey deploys a stateless agent
// template with a managed Postgres thread store and plays both the seller's
// backend and the agent's tool: it asks a question into the agent inbox,
// reads the tool call the agent dispatched, parks the agent, posts the
// tool's result to the callback URL, and reads the answer the woken agent
// wrote. The agent holds nothing while it waits: its function is parked.
// It runs once per template: agent-stateless-node (a hand-written loop) and
// agent-infinity-rust (Infinity's step mode, its tool answering in RAP).

// agentThreadWait bounds one slice's way from an inbox publish to its row:
// a wake, a realtime trigger dispatch and one Postgres transaction.
const agentThreadWait = 3 * time.Minute

type agentJourney struct {
	tg                 *target
	pg                 *postgresJourney // the thread store's database
	shape              agentShape
	app, base, db      string
	key, readToken     string
	endpoint, threadID string
	callbackURL        string
	toolCallID         string
}

// agentShape is what differs between the templates: where the thread view
// shows the dispatched tool call, and what the tool posts back.
type agentShape struct {
	template string
	// toolCall finds the dispatched call's callback URL and ID in the view.
	toolCall func(view map[string]any) (url, id string)
	// result is the tool's answer for one message or tool call.
	result func(j *agentJourney, messageID string) []byte
}

var agentShapes = []agentShape{
	{
		template: "agent-stateless-node",
		toolCall: func(view map[string]any) (string, string) {
			messages, _ := view["messages"].([]any)
			for _, m := range messages {
				if url := str(m, "content.tool_call.callback_url"); url != "" {
					return url, ""
				}
			}
			return "", ""
		},
		result: func(_ *agentJourney, messageID string) []byte {
			body, _ := json.Marshal(map[string]any{"message_id": messageID, "kind": "tool_result", "data": map[string]string{"answer": "Valletta"}})
			return body
		},
	},
	{
		template: "agent-infinity-rust",
		toolCall: func(view map[string]any) (string, string) {
			held, _ := view["held_tool_calls"].([]any)
			for _, c := range held {
				if url := str(c, "callback_url"); url != "" {
					return url, str(c, "id")
				}
			}
			return "", ""
		},
		result: func(j *agentJourney, messageID string) []byte {
			// A RAP tool_result (ADR-20261030125501 §3); its call ID is its identity.
			id := j.toolCallID
			if messageID != "r-1" {
				id = messageID
			}
			body, _ := json.Marshal(map[string]any{"type": "tool_result", "group_id": j.threadID, "id": id, "text": "Valletta"})
			return body
		},
	},
}

// TestStatelessAgent is the two-turn stateless agent journey (row 167), once
// per template.
func TestStatelessAgent(t *testing.T) {
	tg := needs(t, tierFull, true)
	t.Parallel()
	for _, shape := range agentShapes {
		t.Run(shape.template, func(t *testing.T) {
			t.Parallel()
			statelessAgent(t, tg, shape)
		})
	}
}

func statelessAgent(t *testing.T, tg *target, shape agentShape) {
	j := &agentJourney{tg: tg, pg: &postgresJourney{tg: tg, bindings: map[string]string{}}, shape: shape, threadID: "e2e-" + tg.slug("thread")}
	j.available(t)
	j.app = tg.app(t, "agent")
	j.pg.app = j.app
	j.db = tg.slug("agent-db")
	j.readToken = randomHex(t, 24)
	// Registered after the app, so it runs first: an attached database
	// cannot be deleted.
	t.Cleanup(func() { j.pg.purge(t, j.db) })
	s := newStory(t)
	s.step("create_database", func(t *testing.T) {
		tg.ok(t, time.Minute, "postgres", "create", j.db, "--region", j.pg.region)
		j.pg.waitReady(t, j.db)
	})
	s.step("deploy_agent", func(t *testing.T) {
		receipt := tg.deploy(t, j.app, "--template", shape.template, "--no-require-authn")
		j.base = strings.TrimRight(str(receipt, "app_url"), "/")
		if j.base == "" {
			j.base = tg.appURL(j.app)
		}
	})
	s.step("inbox_declared", j.inboxDeclared)
	s.step("attach_store", func(t *testing.T) {
		tg.ok(t, 5*time.Minute, "env", "set", "--app", j.app, "AGENT_READ_TOKEN="+j.readToken, "GREGALE_API_URL="+tg.api)
		j.pg.attach(t, j.db, "DATABASE_URL") // waits for the binding, then restarts fresh
	})
	s.step("ask", j.ask)
	s.step("tool_call_dispatched", j.toolCallDispatched)
	s.step("agent_parks", func(t *testing.T) { tg.ok(t, 2*time.Minute, "app", "park", j.app) })
	s.step("tool_answers", j.toolAnswers)
	s.step("agent_answers", j.agentAnswers)
	s.check("answered_thread_refuses_callbacks", j.callbackRevoked)
}

// available skips unless the account may use agents, realtime and managed
// Postgres: agents is internal until the owner's production test.
func (j *agentJourney) available(t *testing.T) {
	doc := object(t, j.tg.ok(t, time.Minute, "feature", "list").stdout)
	list, _ := field(doc, "capabilities").([]any)
	enabled := map[string]bool{}
	for _, c := range list {
		enabled[str(c, "key")] = str(c, "enabled") == "true"
	}
	for _, key := range []string{"agents", "realtime"} {
		if !enabled[key] {
			t.Skipf("the %s capability is not released to this account", key)
		}
	}
	pg := object(t, j.tg.ok(t, time.Minute, "postgres", "capabilities").stdout)
	if str(pg, "provisioning_enabled") != "true" || str(pg, "region") == "" {
		t.Skipf("managed Postgres provisioning is closed for this account: %s", compact(pg))
	}
	j.pg.region = str(pg, "region")
	j.key = j.tg.scopedKey(t, "agent", "deploy:write")
}

// inboxDeclared finds the endpoint the template's gregale.yaml declared.
func (j *agentJourney) inboxDeclared(t *testing.T) {
	var endpoints []map[string]any
	if err := json.Unmarshal(j.tg.ok(t, time.Minute, "realtime", "list", j.app).stdout, &endpoints); err != nil {
		t.Fatalf("realtime list: %v", err)
	}
	for _, e := range endpoints {
		if str(e, "slug") == "agent" {
			j.endpoint = str(e, "id")
		}
	}
	if j.endpoint == "" {
		t.Fatalf("the deploy declared no agent endpoint: %v", endpoints)
	}
}

// ask publishes the user's question, as the seller's backend does.
func (j *agentJourney) ask(t *testing.T) {
	res := j.tg.apiCall(http.MethodPost, "/v1/apps/"+j.app+"/realtime/endpoints/"+j.endpoint+"/channels/agent-inbox/publish",
		map[string]any{"data": `{"text":"What is the capital of Malta?"}`,
			"metadata": map[string]string{"thread_id": j.threadID, "message_id": "q-1", "kind": "user"}}, j.key)
	if res.status/100 != 2 {
		t.Fatalf("publish: %s", res)
	}
}

// thread reads the agent's view of the thread.
func (j *agentJourney) thread(t *testing.T) map[string]any {
	r := fetch(http.MethodGet, j.base+"/threads/"+j.threadID, map[string]string{"Authorization": "Bearer " + j.readToken})
	if r.status != http.StatusOK {
		return nil
	}
	var doc map[string]any
	if json.Unmarshal(r.body, &doc) != nil {
		return nil
	}
	return doc
}

func threadMessages(view map[string]any) []any {
	messages, _ := view["messages"].([]any)
	return messages
}

// toolCallDispatched waits for the slice that answered the question with a
// tool call, and keeps its callback URL: the journey is that tool.
func (j *agentJourney) toolCallDispatched(t *testing.T) {
	eventually(t, "the agent's tool call", agentThreadWait, 5*time.Second, func() (bool, string) {
		view := j.thread(t)
		j.callbackURL, j.toolCallID = j.shape.toolCall(view)
		return j.callbackURL != "", compact(view)
	})
	if !strings.HasPrefix(j.callbackURL, j.tg.api+"/v1/agent-callbacks/") {
		t.Fatalf("callback URL %q is not on %s", j.callbackURL, j.tg.api)
	}
}

// toolAnswers posts the tool's result while the agent is parked.
func (j *agentJourney) toolAnswers(t *testing.T) {
	r := j.postCallback("r-1")
	if r.status != http.StatusAccepted {
		t.Fatalf("callback post: %s", r)
	}
	if again := j.postCallback("r-1"); again.status != http.StatusAccepted {
		t.Fatalf("a repeated message_id: %s, want it to land once with 202", again)
	}
}

func (j *agentJourney) postCallback(messageID string) httpResult {
	return send(http.MethodPost, j.callbackURL, map[string]string{"Content-Type": "application/json"}, j.shape.result(j, messageID))
}

// agentAnswers waits for the woken agent's answer, written once.
func (j *agentJourney) agentAnswers(t *testing.T) {
	eventually(t, "the agent's answer", agentThreadWait, 5*time.Second, func() (bool, string) {
		messages := threadMessages(j.thread(t))
		answers := 0
		for _, m := range messages {
			if strings.Contains(str(m, "content.text"), "Valletta") {
				answers++
			}
		}
		return answers == 1 && len(messages) == 4, compact(messages)
	})
}

// callbackRevoked checks that answering closed the thread to its URLs.
func (j *agentJourney) callbackRevoked(t *testing.T) {
	eventually(t, "the callback URL refused", time.Minute, 5*time.Second, func() (bool, string) {
		r := j.postCallback("r-2")
		return r.status == http.StatusNotFound, r.String()
	})
}
