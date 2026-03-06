---
name: heartbeat
description: Proactive check-in skill. Runs on a schedule to surface things worth the user's attention without being asked.
category: proactive
triggers: [heartbeat, proactive, check in, check-in]
cron:
  interval: 30m
  prompt: "Run your heartbeat check. Review ongoing_context and recent memory. If something warrants reaching out — a follow-up, a pending commitment, something you noticed — say it concisely. If nothing warrants a message, respond with exactly: QUIET"
  channel: default
---

You are running a scheduled heartbeat — a proactive check-in without any inbound message from the user.

## What to do

1. **Check ongoing context** using `note_context` — look for open threads, things you said you'd follow up on, or items the user asked you to watch.

2. **Check active intents** using `list_intents` — are there pending reminders or scheduled tasks the user should know about?

3. **Decide whether to speak** — only reach out if there's something genuinely worth saying.

## Critical rule

**After using any tool**, you MUST produce a text response. That response is either:
- Something concise and useful to tell the user, OR
- The exact word: `QUIET`

The suppression word is spelled Q-U-I-E-T. Not QUICK. Not quiet. Not Quiet. Exactly: **QUIET**

Do NOT make additional tool calls after checking intents and context.
Do NOT write any summary or explanation before or after QUIET.
Your entire response must be either something useful to say, or the single word QUIET.

## When to speak

- You noticed something the user asked you to watch
- A follow-up is overdue
- There's a pending intent the user may have forgotten about

## When to say QUIET

- Nothing new since the last heartbeat
- You checked intents and context and found nothing urgent
- You have nothing substantive to add

## Response

Either respond with something genuinely useful, or respond with exactly:

QUIET

Nothing else. No other text alongside QUIET. The daemon suppresses QUIET and does not deliver it to the user.
