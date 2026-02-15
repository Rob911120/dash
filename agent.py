#!/usr/bin/env python3
"""Minimal Claude agent with continuous chat and dash tool integration."""

import json
import subprocess
import sys
import os

import anthropic

DASHMCP = "/dash/.claude/mcp/dashmcp"
MODEL = os.environ.get("DASH_MODEL", "claude-opus-4-6")

# --- Tool definitions (curated subset) ---

TOOLS = [
    {
        "name": "working_set",
        "description": "Get the current working set: mission, context frame, active tasks, constraints, recent insights/decisions.",
        "input_schema": {"type": "object", "properties": {}},
    },
    {
        "name": "tasks",
        "description": "View active tasks with dependencies and intent links. Shows which tasks are blocked, what blocks them, and which intent motivates each task.",
        "input_schema": {"type": "object", "properties": {}},
    },
    {
        "name": "remember",
        "description": "Save an insight, decision, or todo to the graph.",
        "input_schema": {
            "type": "object",
            "required": ["type", "text"],
            "properties": {
                "type": {"type": "string", "enum": ["insight", "decision", "todo"], "description": "Type of note"},
                "text": {"type": "string", "description": "The content to remember"},
                "context": {"type": "string", "description": "Additional context"},
            },
        },
    },
    {
        "name": "forget",
        "description": "Soft-delete remembered nodes (insight, decision, todo) from the context. Search by name or content text.",
        "input_schema": {
            "type": "object",
            "required": ["query"],
            "properties": {
                "query": {"type": "string", "description": "Search term to match node name or content"},
                "type": {"type": "string", "enum": ["insight", "decision", "todo"], "description": "Only match nodes of this type"},
                "dry_run": {"type": "boolean", "description": "Show what would be deleted without actually deleting"},
            },
        },
    },
    {
        "name": "summary",
        "description": "Get project overview: active tasks, recent sessions, most touched files.",
        "input_schema": {
            "type": "object",
            "properties": {
                "scope": {"type": "string", "enum": ["all", "tasks", "recent", "files"], "description": "What to include (default: all)"},
                "hours": {"type": "integer", "description": "Time window in hours (default: 24)"},
            },
        },
    },
    {
        "name": "search",
        "description": "Semantic search over files using embeddings. Find files related to a concept or query.",
        "input_schema": {
            "type": "object",
            "required": ["query"],
            "properties": {
                "query": {"type": "string", "description": "Natural language query to search for"},
                "limit": {"type": "integer", "description": "Maximum results (default: 10)"},
            },
        },
    },
    {
        "name": "context_pack",
        "description": "Assemble a ranked context pack combining semantic similarity, recency, frequency, and graph proximity.",
        "input_schema": {
            "type": "object",
            "required": ["query"],
            "properties": {
                "query": {"type": "string", "description": "Natural language query to search for"},
                "profile": {"type": "string", "enum": ["task", "plan", "default"], "description": "Retrieval profile"},
                "task_name": {"type": "string", "description": "Optional task name for graph proximity boosting"},
            },
        },
    },
    {
        "name": "suggest_improvement",
        "description": "Record a concrete improvement, gap, or architectural flaw identified during conversation.",
        "input_schema": {
            "type": "object",
            "required": ["title", "description", "rationale"],
            "properties": {
                "title": {"type": "string", "description": "Concise title"},
                "description": {"type": "string", "description": "Detailed technical description"},
                "rationale": {"type": "string", "description": "Why is this important?"},
                "priority": {"type": "string", "enum": ["low", "medium", "high", "critical"]},
                "affected_component": {"type": "string", "description": "File path or component name"},
            },
        },
    },
    {
        "name": "query",
        "description": "Execute a read-only SQL query against the dash graph database. Tables: nodes, edges, edge_events, observations.",
        "input_schema": {
            "type": "object",
            "required": ["query"],
            "properties": {
                "query": {"type": "string", "description": "SQL SELECT query to execute"},
            },
        },
    },
    {
        "name": "plan",
        "description": "Manage implementation plans. Operations: create, advance, update, get, list.",
        "input_schema": {
            "type": "object",
            "required": ["op"],
            "properties": {
                "op": {"type": "string", "enum": ["create", "advance", "update", "get", "list"], "description": "Operation"},
                "id": {"type": "string", "description": "Plan UUID (for advance/update/get)"},
                "name": {"type": "string", "description": "Plan name in kebab-case"},
                "data": {"type": "object", "description": "Plan data (for create/update)"},
            },
        },
    },
    {
        "name": "read",
        "description": "Read the contents of a file. Returns lines with line numbers.",
        "input_schema": {
            "type": "object",
            "required": ["path"],
            "properties": {
                "path": {"type": "string", "description": "Absolute path to the file"},
                "offset": {"type": "integer", "description": "Line number to start from (1-based)"},
                "limit": {"type": "integer", "description": "Maximum lines to return (default: 2000)"},
            },
        },
    },
    {
        "name": "exec",
        "description": "Execute a shell command. Returns stdout, stderr, and exit code.",
        "input_schema": {
            "type": "object",
            "required": ["command"],
            "properties": {
                "command": {"type": "string", "description": "Shell command to execute"},
                "timeout_ms": {"type": "integer", "description": "Timeout in milliseconds (default: 30000)"},
                "cwd": {"type": "string", "description": "Working directory"},
            },
        },
    },
    {
        "name": "activity",
        "description": "Get recent Claude Code session activity. Shows sessions with file counts, duration, and project paths.",
        "input_schema": {
            "type": "object",
            "properties": {
                "limit": {"type": "integer", "description": "Maximum sessions to return (default: 10, max: 50)"},
            },
        },
    },
    {
        "name": "update_state_card",
        "description": "Update the project state card. Free-text summary of current focus, active work, and key context.",
        "input_schema": {
            "type": "object",
            "required": ["text"],
            "properties": {
                "text": {"type": "string", "description": "Free-text project state card (10-30 lines)"},
            },
        },
    },
    {
        "name": "prompt",
        "description": "Get an assembled prompt for a named profile. Returns the full system prompt text with dynamic context.",
        "input_schema": {
            "type": "object",
            "required": ["profile"],
            "properties": {
                "profile": {"type": "string", "description": "Profile name (e.g. 'default', 'task', 'planner')"},
                "task_name": {"type": "string", "description": "Task name (for 'task' profile)"},
                "refresh": {"type": "boolean", "description": "Force refresh (skip cache)"},
            },
        },
    },
]


def run_tool(name: str, args: dict) -> str:
    """Call a tool via dashmcp JSON-RPC and return the result text."""
    init_req = {"jsonrpc": "2.0", "method": "initialize", "params": {}, "id": 0}
    call_req = {
        "jsonrpc": "2.0",
        "method": "tools/call",
        "params": {"name": name, "arguments": args},
        "id": 1,
    }
    payload = json.dumps(init_req) + "\n" + json.dumps(call_req) + "\n"

    try:
        proc = subprocess.run(
            [DASHMCP],
            input=payload,
            capture_output=True,
            text=True,
            timeout=60,
        )
    except subprocess.TimeoutExpired:
        return json.dumps({"error": "Tool call timed out after 60s"})

    # Parse the second line (tools/call response)
    lines = [l for l in proc.stdout.strip().split("\n") if l.strip()]
    if len(lines) < 2:
        return json.dumps({"error": "No response from dashmcp", "stderr": proc.stderr[:500]})

    try:
        resp = json.loads(lines[1])
    except json.JSONDecodeError:
        return json.dumps({"error": "Invalid JSON from dashmcp", "raw": lines[1][:500]})

    if "error" in resp:
        return json.dumps(resp["error"])

    # Extract text content from MCP tool result
    result = resp.get("result", {})
    content = result.get("content", [])
    texts = [item.get("text", "") for item in content if item.get("type") == "text"]
    return "\n".join(texts) if texts else json.dumps(result)


def load_system_prompt() -> str:
    """Build system prompt from working_set context."""
    print("  [INIT] Loading context from graph...")
    ws = run_tool("working_set", {})

    return f"""Du är dash-agenten — en AI-assistent kopplad till ett grafsystem som spårar projekt, tasks, insikter och beslut.

Du har tillgång till dash-verktyg för att läsa och skriva till grafen. Använd dem aktivt.

Svara på svenska om inte användaren skriver på engelska.

== AKTUELL KONTEXT ==
{ws}
"""


def format_tool_call(name: str, args: dict) -> str:
    """Format tool call for display."""
    args_str = json.dumps(args, ensure_ascii=False)
    if len(args_str) > 120:
        args_str = args_str[:117] + "..."
    return f"  [TOOL] {name}({args_str})"


def chat():
    client = anthropic.Anthropic()
    messages = []
    system = load_system_prompt()

    # Convert our tool defs to Anthropic API format
    api_tools = [
        {"name": t["name"], "description": t["description"], "input_schema": t["input_schema"]}
        for t in TOOLS
    ]

    print("  [READY] Skriv 'quit' för att avsluta.\n")

    while True:
        try:
            user_input = input("> ")
        except (EOFError, KeyboardInterrupt):
            print("\nHejdå!")
            break

        if user_input.strip().lower() in ("quit", "exit", "q"):
            print("Hejdå!")
            break

        if not user_input.strip():
            continue

        messages.append({"role": "user", "content": user_input})

        # API call with tool use loop
        try:
            response = client.messages.create(
                model=MODEL,
                system=system,
                messages=messages,
                tools=api_tools,
                max_tokens=4096,
            )
        except anthropic.APIError as e:
            print(f"  [ERROR] API: {e}")
            messages.pop()  # Remove failed user message
            continue

        # Tool use loop
        while response.stop_reason == "tool_use":
            tool_blocks = [b for b in response.content if b.type == "tool_use"]
            messages.append({"role": "assistant", "content": response.content})

            tool_results = []
            for block in tool_blocks:
                print(format_tool_call(block.name, block.input))
                result = run_tool(block.name, block.input)
                tool_results.append({
                    "type": "tool_result",
                    "tool_use_id": block.id,
                    "content": result,
                })

            messages.append({"role": "user", "content": tool_results})

            try:
                response = client.messages.create(
                    model=MODEL,
                    system=system,
                    messages=messages,
                    tools=api_tools,
                    max_tokens=4096,
                )
            except anthropic.APIError as e:
                print(f"  [ERROR] API: {e}")
                break

        # Print text response
        text_parts = [b.text for b in response.content if hasattr(b, "text")]
        if text_parts:
            messages.append({"role": "assistant", "content": response.content})
            print("\n" + "\n".join(text_parts) + "\n")


if __name__ == "__main__":
    print("Dash Agent v0.1")
    print(f"Model: {MODEL}")
    print(f"Tools: {len(TOOLS)} loaded\n")
    chat()
