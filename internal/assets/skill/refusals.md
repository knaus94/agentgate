# Refusals and errors

A command that is not legal now answers with the task and `refused: {action, because}`; one that could not do
what it was asked answers with `error`. Nothing half-done is left: the object shows the task as it is, and
`actions` says what is possible. A refusal that depends on a value lists the valid ones.

| The text says | Why | What to do |
|---|---|---|
| `an operation is already running` | another call of this session is at work | wait for its answer; `status` shows `running`; `tell` reaches running writers; `stop` ends it |
| `there is no blocked writer to answer` | no writer waits for an answer | settle `needs`, or `run` |
| `work is given once the writers of the round have reported` | `decide {work}` while writers are at work or blocked | `answer` a blocked writer, `run` a failed one, then give work |
| `… is not a stream of the present position; streams: …` | a stream name of another stage, or a typo | use one of those listed |
| `finding "…" is not a finding of this task; open ones: …` | an id of an earlier stage, or a typo | use one listed; a note of an earlier stage is given as `work` |
| `"…" is not an unmet requirement or rule of the present assessment; unmet: …` | the criterion is verified, or not due here | pick one listed, or give plain `work` |
| `decide {…} was replaced in 0.17` | an input of an earlier protocol | `work: [{stream, note, requirement?, escalate?}]` |
| `the amendment changes or removes requirements …` | a requirement's text changed or it was removed | move it with `requirements: [{id, stage}]`, add an intermediate one, or put the user's words in `authorized` |
| `stage "…" is accepted and cannot change` | an amendment touched an accepted stage | change only the present and later stages |
| `… cannot leave its scope` | a stream lost a file it already changed | keep the file in its scope |
| `… could name the same file; streams must be disjoint` | two streams of one stage overlap | narrow one of the two entries named |
| `files are owned by another session's task` | a shared checkout | wait for that task, or ask the user about `-w` |
| `this session already has an unfinished task` | `prepare` with a new request while a task is open | continue it (`status`), revise it with `prepare {notes}`, or end it on the user's word |
| `cannot amend while a writer is running` | — | wait for the call that runs it |
| `land {how: commit} needs message` | — | a commit message in the repository's convention (its `git log`) |
| `the checkout has uncommitted changes in files this task would commit` | the user's own edits | tell the user; never stash or discard their work |
| `these files changed after the task's writers left them` | `drop` would overwrite someone's edit | tell the user; see the `finished` page |
| `the user stopped this task` | the user's stop | `resume` only on their word |
| `running inside the Codex sandbox` | a pipe, heredoc or `&&` around `agentgate` | one plain command with `--file` |
| `no reviewer independent of the writer is configured` | the review lane's model writes too | the user changes the table (`models` page) |
| `without a mode flag the plan needs assessment` | a plan with neither | add the assessment, or a mode |

Never edit state, rename findings or recreate a task to get past a refusal: fix the cause, `amend` the plan,
or `pause` with the true reason and report INCOMPLETE.
