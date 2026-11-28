"""Validate server-declared pending qualification against a confirmed receipt."""

from datetime import datetime
import time


def pending_qualification(run):
    q = run.get("qualification")

    def date(value):
        return datetime.fromisoformat(value.replace("Z", "+00:00")).timestamp()

    if (
        run["status"] != "pending"
        or not q
        or not q["conditions"]
        or len(q["conditions"]) > 12800
        or date(q["checked_at"]) > time.time()
        or date(q["checked_at"]) >= date(run["expires_at"])
    ):
        raise ValueError("server did not confirm pending qualification")
    members = list(run["members"].values()) if "members" in run else [run]
    for c in q["conditions"]:
        member = next((m for m in members if m["app"] == c["app"]), None)
        if member is None or date(q["checked_at"]) < date(member["created_at"]):
            raise ValueError("qualification changed receipt selection")
        if c["code"] in ["runtime_updating", "runtime_stale"]:
            if c.get("type") or c.get("name") or c.get("binding") or c.get("scope") != member["scope"]:
                raise ValueError("invalid runtime qualification condition")
            continue
        allowed = (
            c["code"] in ["queue_consumer_unobserved", "queue_consumer_stale"]
            and c.get("type") == "queue"
            or c["code"]
            in ["application_adoption_unknown", "application_ack_stale", "application_ack_candidate_unobserved"]
            and c.get("type") in ["postgres", "object_storage"]
        )
        candidates = [s["plan"] for s in member["steps"]] + member["inventory"]["bindings"]
        if not allowed or not any(
            all(p.get(k) == c.get(k) for k in ["type", "name", "binding", "scope"]) for p in candidates
        ):
            raise ValueError("invalid or unselected qualification condition")
    # Expose only declared fields through progress callbacks.
    return dict(
        checked_at=q["checked_at"],
        conditions=[
            {k: v for k, v in c.items() if k in ["app", "code", "type", "name", "binding", "scope"]}
            for c in q["conditions"]
        ],
    )
