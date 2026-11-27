# A tool call sent right after a reply can be refused as busy

**Created**: 2026-11-27

**Status**: Open; accepted contract behaviour, not changed.

## Problem

One tool is admitted until its response write completes. A client can read a reply before the server has finished that
write and released the slot, so a tool call sent at once is answered `busy`. A native feature 007 test assumed the
slot was free and failed on loaded CI machines; under a deliberately loaded WSL2 comparison the same happened on the
code before the feature 007 corrections (48 of 119 runs). The test now retries.

Clients that wait for model output between calls are unlikely to see it. A client that pipelines calls would.

## Options

| Option | Cost |
| --- | --- |
| Leave it; `busy` already asks the client to retry (current) | A fast client sees avoidable refusals. |
| Wait briefly for a completing write before refusing | Changes the inherited admission rule of no waiting tool call. |

## Review trigger

A client report of unexpected `busy` answers, or before the first release.
