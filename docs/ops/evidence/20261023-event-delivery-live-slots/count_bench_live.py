"""EventDeliveryCounts as main reads it (every slot of the account) and as the
live-slots branch reads it (the account's live slots, event_delivery_slots_live),
as the account's retained history and the platform's live backlog grow.

Runs on gregale-internal-test-1 against a disposable database loaded from the
branch's schema.sql: python3 -I count_bench_live.py <psql> <database> fresh|stale.
Seeding skips triggers and foreign keys (session_replication_role=replica) and
sets each slot's live flag from its invocation's state, as the triggers do.
"""
import json
import statistics
import subprocess
import sys

PSQL, DB = sys.argv[1], sys.argv[2]
ACCOUNT = "11111111-1111-4111-8111-111111111111"
NOISE = "22222222-2222-4222-8222-222222222222"
APP1, APP2 = "aaaaaaaa-0000-4000-8000-000000000001", "aaaaaaaa-0000-4000-8000-000000000002"
NOISE_APP = "bbbbbbbb-0000-4000-8000-000000000001"
LIVE = 50
RETAINED = [0, 10_000, 100_000, 500_000]
NOISE_RETAINED = 200_000
REPEATS = 9

COUNTS = """SELECT count(*)::bigint AS account_count,
 count(*) FILTER (WHERE s.app_id=$2)::bigint AS app_count,
 count(*) FILTER (WHERE s.app_id=$2 AND s.subscription_id=$3)::bigint AS consumer_count
FROM event_delivery_slots s JOIN invocations i ON i.id=s.invocation_id
WHERE s.account_id=$1 {live} AND i.state IN ('pending','dispatching')
 AND NOT ($4 AND i.state='pending' AND i.app_id=$2
   AND coalesce(i.work_policy_name,'')=$5 AND i.work_key_digest=$6)"""
MAIN = COUNTS.format(live="")
BRANCH = COUNTS.format(live="AND s.live")
ARGS = f"('{ACCOUNT}','{APP1}','sub-1',false,'',''::bytea)"
TYPES = "(uuid,uuid,text,boolean,text,bytea)"


def psql(sql):
    out = subprocess.run([*PSQL.split(), "-d", DB, "-v", "ON_ERROR_STOP=1", "-Atq"], input=sql,
                         capture_output=True, text=True, check=True)
    return out.stdout


def seed(account, apps, state, count):
    live = "true" if state in ("pending", "dispatching") else "false"
    psql(f"""SET session_replication_role = replica;
WITH made AS (
  INSERT INTO invocations (id, app_id, account_id, source, deployment_scope, state)
  SELECT gen_random_uuid(), (ARRAY['{"','".join(apps)}']::uuid[])[1 + g % {len(apps)}], '{account}',
         'async_invoke', 'production', '{state}'
  FROM generate_series(1, {count}) g
  RETURNING id, app_id, account_id)
INSERT INTO event_delivery_slots (invocation_id, account_id, app_id, subscription_id, live)
SELECT id, account_id, app_id, CASE WHEN app_id = '{APP1}' THEN 'sub-1' ELSE 'sub-2' END, {live} FROM made;""")


def plan_shape(node):
    label = node["Node Type"] + (f" {node['Index Name']}" if "Index Name" in node else "") + \
        (f" on {node['Relation Name']}" if "Relation Name" in node and "Index Name" not in node else "")
    children = [plan_shape(c) for c in node.get("Plans", [])]
    return label if not children else f"{label} ({'; '.join(children)})"


def measure(name, query):
    # Prepared, as pgx runs sqlc queries; the first runs warm the plan cache.
    script = f"PREPARE q{TYPES} AS {query};\n" + f"EXECUTE q{ARGS};\n" * 6
    script += f"EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) EXECUTE q{ARGS};\n" * REPEATS
    lines = psql(script).strip().splitlines()
    counts, body = lines[0], "\n".join(lines[6:])
    decoder, pos, plans = json.JSONDecoder(), 0, []
    while pos < len(body):
        while pos < len(body) and body[pos].isspace():
            pos += 1
        if pos >= len(body):
            break
        value, pos = decoder.raw_decode(body, pos)
        plans.append(value[0])
    top = plans[-1]["Plan"]
    return {"query": name, "counts": counts,
            "execution_ms": round(statistics.median(p["Execution Time"] for p in plans), 3),
            "planning_ms": round(statistics.median(p.get("Planning Time", 0.0) for p in plans), 3),
            "buffers": top.get("Shared Hit Blocks", 0) + top.get("Shared Read Blocks", 0), "plan": plan_shape(top)}


def apps():
    psql(f"""SET session_replication_role = replica;
INSERT INTO apps (id, account_id, slug, ram_mb) VALUES
 ('{APP1}', '{ACCOUNT}', 'count-a', 512), ('{APP2}', '{ACCOUNT}', 'count-b', 512),
 ('{NOISE_APP}', '{NOISE}', 'count-noise', 512);""")


def compare(results, **scenario):
    for name, query in (("every slot (main)", MAIN), ("live slots (branch)", BRANCH)):
        row = measure(name, query)
        row.update(scenario)
        results.append(row)
        print(json.dumps(row), flush=True)


FRESH = "VACUUM ANALYZE invocations; VACUUM ANALYZE event_delivery_slots; ANALYZE apps;"
results, mode = [], sys.argv[3]
if mode == "fresh":
    apps()
    seed(ACCOUNT, [APP1], "pending", LIVE)
    seed(NOISE, [NOISE_APP], "pending", LIVE)
    seed(NOISE, [NOISE_APP], "completed", NOISE_RETAINED)
    have = 0
    for retained in RETAINED:
        if retained > have:
            seed(ACCOUNT, [APP1, APP2], "completed", retained - have)
            have = retained
        psql(FRESH)
        compare(results, stats="fresh", retained_slots=retained, live_deliveries=LIVE, other_live=LIVE)
    other = LIVE
    for other_live in (10_000, 50_000):
        seed(NOISE, [NOISE_APP], "pending", other_live - other)
        other = other_live
        psql(FRESH)
        compare(results, stats="fresh", retained_slots=have, live_deliveries=LIVE, other_live=other_live)
    # Joint statistics on (account_id, live): an account with a long history
    # has few live slots, which independent column statistics cannot say.
    psql("CREATE STATISTICS event_delivery_slots_account_live (mcv, dependencies) ON account_id, live FROM event_delivery_slots; ANALYZE event_delivery_slots;")
    compare(results, stats="fresh + (account_id, live) statistics", retained_slots=have, live_deliveries=LIVE, other_live=other)
    seed(NOISE, [NOISE_APP], "pending", 10_000)
    psql("ANALYZE event_delivery_slots; ANALYZE invocations;")
    compare(results, stats="fresh + (account_id, live) statistics", retained_slots=have, live_deliveries=LIVE, other_live=other + 10_000)
elif mode == "stale":
    psql("ALTER TABLE invocations SET (autovacuum_enabled = false); "
         "ALTER TABLE event_delivery_slots SET (autovacuum_enabled = false); "
         "ALTER TABLE apps SET (autovacuum_enabled = false);")
    psql(FRESH)
    apps()
    for retained in (2_000, 100_000):
        seed(ACCOUNT, [APP1], "pending", LIVE)
        seed(ACCOUNT, [APP1, APP2], "completed", retained)
        seed(NOISE, [NOISE_APP], "pending", 2_000)
        seed(NOISE, [NOISE_APP], "completed", 20_000)
        compare(results, stats="stale (analyzed empty)", retained_slots=retained, live_deliveries=LIVE, other_live=2_000)
        psql("SET session_replication_role = replica; TRUNCATE event_delivery_slots, invocations CASCADE;")

print(json.dumps({"postgres": psql("SHOW server_version;").strip(), "mode": mode, "results": results}))
