#!/usr/bin/env python3
"""Compatibility check: unmodified official clients against delos-gateway.

Run through run.sh, which starts the mock upstream and the gateway:

    uv run --with openai --with anthropic tests/compat/python/compat_test.py

The point is not to test the mock; it is to prove that the OpenAI and
Anthropic client libraries, with nothing changed but base_url, work against
the gateway. If a client release breaks us, this fails before users notice.
"""

import os
import sys

import anthropic
import openai

GATEWAY = os.environ.get("DELOS_GATEWAY_URL", "http://127.0.0.1:8099")
MODEL = os.environ.get("DELOS_COMPAT_MODEL", "delos-mock")
API_KEY = os.environ.get("DELOS_API_KEY", "delos-dev")

failures = []


def check(name, condition, detail=""):
    if condition:
        print(f"  ok   {name}")
    else:
        print(f"  FAIL {name}: {detail}")
        failures.append(name)


def openai_surface():
    print(f"openai-python {openai.__version__} -> {GATEWAY}/v1")
    client = openai.OpenAI(base_url=f"{GATEWAY}/v1", api_key=API_KEY, max_retries=0)

    models = client.models.list()
    ids = [m.id for m in models.data]
    check("models.list contains the served model", MODEL in ids, f"got {ids}")

    completion = client.chat.completions.create(
        model=MODEL,
        messages=[{"role": "user", "content": "hello from python"}],
    )
    content = completion.choices[0].message.content
    check("chat.completions.create round-trips the prompt",
          content == "echo: hello from python", f"got {content!r}")
    check("chat.completions.create reports usage",
          completion.usage is not None and completion.usage.total_tokens > 0,
          f"got {completion.usage}")
    check("chat.completions.create finish_reason is stop",
          completion.choices[0].finish_reason == "stop",
          f"got {completion.choices[0].finish_reason}")

    stream = client.chat.completions.create(
        model=MODEL,
        messages=[{"role": "user", "content": "streamed hello"}],
        stream=True,
    )
    chunks = []
    finish_reasons = []
    for event in stream:
        if not event.choices:
            continue
        delta = event.choices[0].delta
        if delta and delta.content:
            chunks.append(delta.content)
        if event.choices[0].finish_reason:
            finish_reasons.append(event.choices[0].finish_reason)
    check("streaming yields multiple deltas", len(chunks) > 1, f"got {chunks}")
    check("streaming reassembles the completion",
          "".join(chunks) == "echo: streamed hello", f"got {''.join(chunks)!r}")
    check("streaming terminates with a finish_reason",
          finish_reasons == ["stop"], f"got {finish_reasons}")

    embeddings = client.embeddings.create(model=MODEL, input="embed me")
    check("embeddings.create returns a vector",
          len(embeddings.data) == 1 and len(embeddings.data[0].embedding) == 4,
          f"got {embeddings.data}")


def anthropic_surface():
    print(f"anthropic {anthropic.__version__} -> {GATEWAY}/v1/messages")
    client = anthropic.Anthropic(base_url=GATEWAY, api_key=API_KEY, max_retries=0)

    message = client.messages.create(
        model=MODEL,
        max_tokens=64,
        messages=[{"role": "user", "content": "hello from anthropic"}],
    )
    text = "".join(block.text for block in message.content if block.type == "text")
    check("messages.create round-trips the prompt",
          text == "echo: hello from anthropic", f"got {text!r}")
    check("messages.create reports a stop reason",
          message.stop_reason == "end_turn", f"got {message.stop_reason}")
    check("messages.create reports usage",
          message.usage.input_tokens > 0 and message.usage.output_tokens > 0,
          f"got {message.usage}")

    # The SDK's streaming helper accumulates events into a Message, which is
    # strict about the shape of every event - a good conformance check.
    collected = []
    with client.messages.stream(
        model=MODEL,
        max_tokens=64,
        messages=[{"role": "user", "content": "streamed anthropic"}],
    ) as stream:
        for chunk in stream.text_stream:
            collected.append(chunk)
        final = stream.get_final_message()
    check("messages.stream reassembles the completion",
          "".join(collected) == "echo: streamed anthropic", f"got {''.join(collected)!r}")
    check("messages.stream produces a final message",
          final.stop_reason == "end_turn", f"got {final.stop_reason}")


def run(name, fn):
    """Run one surface; an exception from the client library is a failure,
    not a crash - the report has to stay readable."""
    try:
        fn()
    except Exception as exc:  # noqa: BLE001 - any client error is a failure
        print(f"  FAIL {name} surface raised {type(exc).__name__}: {exc}")
        failures.append(f"{name} surface")


def main():
    run("openai", openai_surface)
    run("anthropic", anthropic_surface)
    if failures:
        print(f"\n{len(failures)} compatibility check(s) failed: {failures}")
        return 1
    print("\nall python client compatibility checks passed")
    return 0


if __name__ == "__main__":
    sys.exit(main())
