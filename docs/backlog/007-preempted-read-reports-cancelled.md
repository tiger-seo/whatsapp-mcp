# A read the store preempts reports `cancelled`

**Created**: 2026-11-27

**Status**: Open; found by the feature 007 design review, not reproduced.

## Problem

When the store cancels a tool read itself (ordinary work waiting, too many readers, an oversized WAL, store closing),
the client is told `cancelled`, which the tool contract uses for a client or connection cancellation. `busy`, which
asks the client to retry, describes it better. The behaviour predates feature 007's corrections in the usual case.

## Option

Cancel with a cause and map the store's own preemption to `busy`. This changes tool error behaviour, so it needs a
contract update.

## Review trigger

A change to tool admission or preemption, or before the first release.
