"""Temporary: build smaller copies of the two message-search scale tests.

Usage: python3 scale_variant.py MESSAGES OUTDIR

Writes the copies and a `go test -overlay` file into OUTDIR. The repository's
tests are not changed. The 12 sparse matches keep their relative positions.
"""

import json
import os
import sys

messages, out = int(sys.argv[1]), os.path.abspath(sys.argv[2])
step = messages * 3 // 40
last = 12 * step
assert 0 < last < messages and messages % 1000 == 0 and step * 40 == messages * 3


def replace(text, old, new, count):
    assert text.count(old) == count, (old, text.count(old))
    return text.replace(old, new)


def acceptance(text):
    text = replace(text, "start <= 400000; start += 1000", f"start <= {messages}; start += 1000", 1)
    text = replace(text, "i <= 360000 && i%30000 == 0", f"i <= {last} && i%{step} == 0", 1)
    return replace(text, "360000-i*30000", f"{last}-i*{step}", 1)


def store(text):
    text = replace(text, "WHERE i<400000)", f"WHERE i<{messages})", 1)
    text = replace(text, "i<=360000 AND i%30000=0", f"i<={last} AND i%{step}=0", 2)
    text = replace(text, "360000-i*30000", f"{last}-i*{step}", 1)
    # The closing check needs more rows than one search may visit, so it only
    # holds at the original size.
    start = "\tif _, err := f.store.writer.ExecContext(t.Context(), `INSERT INTO messages(chat_jid,id,sender_jid,time,from_me,text,text_fold) VALUES(?1,'extra-1'"
    end = '\t\tt.Fatalf("unfinished search: %v %v", keys, err)\n\t}\n'
    assert text.count(start) == 1 and text.count(end) == 1
    return text[: text.index(start)] + text[text.index(end) + len(end) :]


os.makedirs(out, exist_ok=True)
overlay = {}
for path, change in (
    ("internal/mcp/message_acceptance_test.go", acceptance),
    ("internal/store/tool_message_search_test.go", store),
):
    with open(path, encoding="utf-8", newline="") as source:
        text = change(source.read())
    target = os.path.join(out, os.path.basename(path))
    with open(target, "w", encoding="utf-8", newline="\n") as variant:
        variant.write(text)
    overlay[os.path.abspath(path)] = target
with open(os.path.join(out, "overlay.json"), "w", encoding="utf-8") as listing:
    json.dump({"Replace": overlay}, listing)
print(f"variant messages={messages} sparse_step={step} newest_match={last} overlay={os.path.join(out, 'overlay.json')}")
