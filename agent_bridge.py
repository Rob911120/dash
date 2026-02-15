#!/usr/bin/env python3
"""Bridge: cockpit (Go) -> Claude Code SDK -> SSE stdout.

Reads OpenAI-format JSON payload from stdin.
No MCP tools — tool definitions come via system prompt, tool calls
are emitted as SSE events for Go-side execution.
Outputs OpenAI-compatible SSE events to stdout.
"""

import asyncio
import json
import sys

from claude_code_sdk import (
    query,
    ClaudeCodeOptions,
    AssistantMessage,
    ResultMessage,
    TextBlock,
    ThinkingBlock,
    ToolUseBlock,
)


def emit(data):
    """Write one SSE event to stdout."""
    sys.stdout.write(f"data: {json.dumps(data, ensure_ascii=False)}\n")
    sys.stdout.flush()


def emit_done(prompt_tok=0, comp_tok=0):
    """Emit usage + DONE sentinel."""
    if prompt_tok or comp_tok:
        emit({
            "choices": [],
            "usage": {
                "prompt_tokens": prompt_tok,
                "completion_tokens": comp_tok,
                "total_tokens": prompt_tok + comp_tok,
            },
        })
    sys.stdout.write("data: [DONE]\n")
    sys.stdout.flush()


def build_prompt(messages):
    """Build prompt with conversation context from OpenAI-format messages.

    Returns (system_prompt, user_prompt).
    """
    system = ""
    history = []

    for msg in messages:
        role = msg.get("role", "")
        content = msg.get("content", "")
        if role == "system":
            system = content
        elif role == "user":
            history.append(("User", content))
        elif role == "assistant" and content:
            history.append(("Assistant", content))

    if not history:
        return system, ""

    last = history[-1]
    if last[0] != "User":
        return system, last[1]

    # Single user message — no context needed
    if len(history) == 1:
        return system, last[1]

    # Multi-turn: include prior conversation as context
    context = "\n\n".join(f"{role}: {text}" for role, text in history[:-1])
    return system, f"<conversation_history>\n{context}\n</conversation_history>\n\n{last[1]}"


async def main():
    payload = json.load(sys.stdin)

    model = payload.get("model", "claude-opus-4-6")
    if "/" in model:
        model = model.split("/", 1)[1]

    system, prompt = build_prompt(payload.get("messages", []))

    if not prompt:
        emit({"choices": [{"delta": {"content": "Error: No user message"}}]})
        emit_done()
        return

    options = ClaudeCodeOptions(
        system_prompt=system or None,
        model=model,
        include_partial_messages=True,
        cwd="/dash",
    )

    # Delta tracking for partial messages
    text_lens = {}   # block_index -> chars emitted
    think_lens = {}  # block_index -> chars emitted
    seen_tools = set()
    was_assistant = False

    try:
        async for message in query(prompt=prompt, options=options):
            if isinstance(message, AssistantMessage):
                if not was_assistant:
                    text_lens.clear()
                    think_lens.clear()
                    seen_tools.clear()
                was_assistant = True

                for i, block in enumerate(message.content):
                    if isinstance(block, TextBlock):
                        prev = text_lens.get(i, 0)
                        if len(block.text) > prev:
                            emit({"choices": [{"delta": {"content": block.text[prev:]}}]})
                            text_lens[i] = len(block.text)
                    elif isinstance(block, ThinkingBlock):
                        prev = think_lens.get(i, 0)
                        if len(block.thinking) > prev:
                            emit({"choices": [{"delta": {"reasoning": block.thinking[prev:]}}]})
                            think_lens[i] = len(block.thinking)
                    elif isinstance(block, ToolUseBlock):
                        if i not in seen_tools:
                            seen_tools.add(i)
                            # Emit as OpenAI-format tool call for Go-side execution
                            emit({"choices": [{"delta": {
                                "tool_calls": [{
                                    "index": 0,
                                    "id": block.id,
                                    "type": "function",
                                    "function": {
                                        "name": block.name,
                                        "arguments": json.dumps(block.input) if isinstance(block.input, dict) else str(block.input),
                                    },
                                }],
                            }}]})
            else:
                was_assistant = False
                if isinstance(message, ResultMessage):
                    inp = 0
                    out = 0
                    if message.usage:
                        inp = message.usage.get("input_tokens", 0)
                        out = message.usage.get("output_tokens", 0)
                    emit_done(inp, out)
                    return
    except Exception as e:
        emit({"choices": [{"delta": {"content": f"\nSDK Error: {e}\n"}}]})

    emit_done()


if __name__ == "__main__":
    asyncio.run(main())
