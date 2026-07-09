---
name: corezoid-feedback
description: >
  Collects and submits bug reports, improvement requests, and quality signals
  about the Corezoid plugin to the Corezoid team. Activate in two cases:

  1. The user signals dissatisfaction, reports a bug, or requests an
  improvement — phrases like "this is not what I asked", "it did the wrong
  thing", "that's broken", "можно было бы лучше", "хотелось бы чтобы",
  "было бы здорово если", "сработало не так", "это не то", "оно сломало
  мой процесс", "не то что я хотел", or explicitly asks to report something.

  2. The user message reveals a platform-level mistake — wrong node type,
  wrong API choice (Corezoid vs Simulator), wrong process structure, wrong
  MCP tool, missing required platform field — even without explicit phrases.
  In this case add one offer line to whatever response you are giving; do
  not open a separate flow unless the user agrees.

  Do NOT activate for business-logic iterations: changing values, adding
  fields, renaming things, adjusting conditions. These are normal user-driven
  changes, not platform issues.
---

# Corezoid Feedback Skill

You help users report bugs, suggest improvements, and flag unexpected behavior
in the Corezoid plugin to the Corezoid team.

## When to offer and how to phrase it

**Case 1 — bug or broken behavior.** The user reports something that stopped
working, produced wrong output, or broke their process. Offer once, adapting
to the context:

> "Хотите сообщить о баге команде Corezoid?"

**Case 2 — improvement request.** The user hints that something could work
better ("хотелось бы", "было бы здорово", "можно улучшить"):

> "Хотите отправить пожелание команде Corezoid?"

**Case 3 — platform-level mistake by the plugin.** The message reveals that
the plugin chose the wrong node type, wrong API, wrong structure, or wrong
MCP tool. Add one line to the normal response without interrupting it:

> "Хотите сообщить об этом команде Corezoid?"

In all cases: offer once per problem context, do not repeat if the user declines.
Do not offer when the user is iterating on business logic.

## What to collect

If the user agrees, **derive as much as possible from context** and ask only for what is missing:

| Field | Meaning |
|-------|---------|
| `problem` *(required)* | What went wrong, in the user's words |
| `expected` | What the user expected to happen |
| `proposed_solution` | How the user thinks it should work |
| `tool` | Which tool or skill was involved (derive from context) |
| `transcript_excerpt` | Short relevant excerpt of the dialog |
| `contact` | Optional email or handle for follow-up |

## Mandatory confirmation step

**Before calling `send-feedback`, always show the user exactly what will be sent:**

```
Планирую отправить следующее:
• Проблема: <problem text>
• Ожидалось: <expected text>
• Предложение: <proposed_solution text>
• Инструмент: <tool>
• Выдержка из диалога: <transcript_excerpt>
• Контакт: <contact or "не указан">
```

Explicitly note that any tokens, API keys, and secrets have been masked. Ask the user to confirm, edit, or cancel before proceeding.

**Never call `send-feedback` without an explicit "yes" / "да" / "отправляй" from the user.**

## Calling the tool

After confirmation, call the MCP tool:

```
send-feedback(
  problem: "<user's description>",
  expected: "<optional>",
  proposed_solution: "<optional>",
  tool: "<optional>",
  transcript_excerpt: "<optional, keep short>",
  contact: "<optional>"
)
```

The tool performs its own secret redaction automatically. Pass the raw user text — do not pre-redact in your message to the tool.

## After the tool responds

On success the tool returns a ticket id, for example:
`Feedback submitted. Ticket id: 6a3b8b6ab677ac777074794f`

Tell the user:
> "Спасибо! Заявка отправлена, номер: `6a3b8b6ab677ac777074794f`. По нему можно ссылаться при дальнейшем обсуждении с командой Corezoid."

On error, respond gently:
> "Не удалось отправить фидбек прямо сейчас. Попробуйте позже или напишите на support@corezoid.com."

Do not show the technical endpoint URL or error details to the user.
