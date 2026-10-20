---
name: baton-handoff
description: Write the narrative part of a Baton Handoff and save it with `baton handoff save`, so that the next Agent or Profile can continue this Task. Use it when the user asks to hand the work over (hand over, hand off, switch to another Agent or Profile, "my limit is running out, continue elsewhere") or runs /baton-handoff, and when the first message of a Session says the narrative part of a Handoff is missing.
---

# Hand the Task over

Baton moves a Task from one Agent or Profile to another with `baton handoff`. It collects the facts itself: the branch, HEAD, `git status`, `git diff --stat` and the last commands. What it cannot know is your reasoning. You write that part, the narrative, and save it. Do not repeat the facts in it.

## 1. Write the narrative

Write Markdown for a reader who knows nothing of this conversation and will start from it. Use these sections, in this order, and keep each one short and concrete:

1. **Goal**: what the Task is for, in a few sentences, and what "done" looks like.
2. **Done so far**: what works now, with file paths and function names where they help. Say what is committed and what is not.
3. **Decisions and why**: each decision that shaped the work, with the reason for it.
4. **Rejected options and why**: what you tried or considered and dropped, so that nobody tries it again.
5. **Next step**: exactly one concrete step to take first (a command to run, a file to edit, a failing test to fix), not a list of hopes.
6. **Open questions**: what is undecided or unknown, and who can answer it.
7. **How to verify**: the commands that show the work is right (tests, linters, a manual check) and what they should print.

Never put secrets in it: no tokens, passwords or keys, not even ones you saw in a command.

## 2. Save it

Pipe the Markdown into `baton handoff save`, from inside the project (the Task is found from the current directory). The binary is `/baton/bin/baton` if that file exists (you are in a sandbox or in a Compose dev container), otherwise `~/.baton/bin/baton`, otherwise `baton` from `PATH`:

```sh
BATON=/baton/bin/baton
[ -x "$BATON" ] || BATON="$HOME/.baton/bin/baton"
[ -x "$BATON" ] || BATON="$(command -v baton)"
"$BATON" handoff save <<'NARRATIVE_END'
## Goal
...
NARRATIVE_END
```

The quoted delimiter keeps backticks and `$` in your text as they are. If the text itself contains a line `NARRATIVE_END`, choose another delimiter. An empty narrative is an error.

The command prints where the narrative went. You can run it again to replace it: the last save wins.

## 3. Tell the user

Say that the narrative is saved. If the user is handing over to another Agent, they now end this Session (`/exit`) and run `baton handoff <project>/<task> --to <agent>[@<profile>]` in a terminal, or have already run it: it waits for this Session to end, then opens the next one. The order does not matter, so do not ask about it.

## When you are the receiving Session

If your first message says that the narrative part of a Handoff is missing, the Session before you could not write it (for example its limit ran out). Write it yourself before you start the work:

1. Read the Handoff file named in the message. Its Facts section holds the git state and the last commands, and links the Event file of the Session before you.
2. Read `git log` and `git diff` of the working copy, and the Event file if you can open it.
3. Write the seven sections above from that. Where you can only guess, say so ("probably", "not known") rather than state it as fact: the reader will trust it less, as the header of the Handoff will say that the receiving Session wrote it.
4. Save it as in step 2, then go on with the Task, starting from the next step you wrote.
