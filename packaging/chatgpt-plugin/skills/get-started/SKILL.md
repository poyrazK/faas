---
name: get-started
description: Connect ChatGPT or Codex to Gregale, check what the connection may do, and show the user's apps and what to ask next. Use right after the Gregale plugin is installed, or when the user asks how to start with Gregale.
---

# Get started with Gregale

Gregale runs backend apps (APIs, web apps, workers) in microVMs that pause
when idle and wake on the next request. This plugin reaches Gregale through
its hosted MCP server; every tool is named `gregale_*`.

## 1. Check the connection

Call `gregale_context`. It names the Gregale account and what this
connection may do.

If Gregale is not connected yet, ChatGPT asks the user to connect. Tell them
what happens in the browser:

1. Sign in to Gregale. Without an account, they can sign up with GitHub or
   Google on the same page.
2. Choose what ChatGPT may do: "Deploy and manage apps" or "Inspect only".
3. Choose which tools it gets and which apps it may use, and connect.

The connection lasts while it is used: a week unused, or 30 days after
connecting, ends it, and ChatGPT asks to connect again.

## 2. Show the user's apps

Call `gregale_list_apps`.

- No apps yet: offer to deploy a first small app, such as a FastAPI or
  Express API, with the deploy skill.
- Some apps: list them, and call `gregale_get_health` for each one the user
  cares about to show whether it is serving, parked or failing.

## 3. Say what to ask next

- "Deploy this API to Gregale."
- "Why is my app returning errors?"
- "Roll back my app to its previous release."
- "How do I move my app from Heroku, Render, Railway or Fly.io?"

For questions about Gregale itself (limits, configuration, error codes), use
`gregale_search_docs` and read a result with `gregale_get_docs_page`. Answer
from the docs, with a link, rather than from memory.
