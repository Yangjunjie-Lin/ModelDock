from __future__ import annotations

from openai import OpenAI


def test_models_list(client: OpenAI, chat_model: str) -> None:
    page = client.models.list()
    ids = {model.id for model in page.data}
    assert ids, "RelayDock returned an empty model catalog"
    assert chat_model in ids, f"configured chat alias {chat_model!r} is not listed"


def test_responses_create(client: OpenAI, chat_model: str) -> None:
    response = client.responses.create(
        model=chat_model,
        input="Reply with a short compatibility-test acknowledgement.",
    )
    assert response.id.startswith("resp_")
    assert response.status == "completed"
    assert response.output_text.strip()


def test_responses_stream(client: OpenAI, chat_model: str) -> None:
    stream = client.responses.create(
        model=chat_model,
        input="Stream a short compatibility-test acknowledgement.",
        stream=True,
    )
    event_types: list[str] = []
    deltas: list[str] = []
    for event in stream:
        event_type = getattr(event, "type", "")
        event_types.append(event_type)
        if event_type == "response.output_text.delta":
            deltas.append(getattr(event, "delta", ""))

    assert "response.completed" in event_types
    assert "".join(deltas).strip()


def test_chat_completions_create(client: OpenAI, chat_model: str) -> None:
    completion = client.chat.completions.create(
        model=chat_model,
        messages=[{"role": "user", "content": "Return a short SDK test acknowledgement."}],
    )
    assert completion.id.startswith("chatcmpl_")
    assert completion.choices
    assert (completion.choices[0].message.content or "").strip()


def test_chat_completions_stream(client: OpenAI, chat_model: str) -> None:
    stream = client.chat.completions.create(
        model=chat_model,
        messages=[{"role": "user", "content": "Stream a short SDK test acknowledgement."}],
        stream=True,
    )
    chunks = [chunk.choices[0].delta.content or "" for chunk in stream if chunk.choices]
    assert "".join(chunks).strip()


def test_embeddings_create(client: OpenAI, embedding_model: str) -> None:
    result = client.embeddings.create(
        model=embedding_model,
        input=["RelayDock SDK compatibility test"],
    )
    assert result.object == "list"
    assert len(result.data) == 1
    assert len(result.data[0].embedding) > 0
    assert result.usage.total_tokens > 0

