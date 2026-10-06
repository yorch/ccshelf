---
name: incident-timeline
description: Build a timestamped incident timeline from chat logs, alerts and deploy history. Use during or right after an incident.
---

# Incident timeline

1. Collect the raw events the user provides: alerts, deploys, chat messages, commands run.
2. Normalize every timestamp to UTC and sort ascending.
3. Mark the detection, the first mitigation and the resolution.
4. Output a table: time, source, event, who. Flag gaps longer than ten minutes.

Never invent an event. If a time is unknown, say so.
