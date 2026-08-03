# Test plans

Manual and semi-manual test runs, each as its own document. Test cases are
numbered `TC-001…`, edge cases and defects `EC-001…`.

**Existing plans are never modified.** A retest gets a new plan with a new
number, so that what was actually observed on a given day stays readable later.
Defects found go into `Backlog/` with a reference to the plan, unless they were
fixed in the same branch — in which case the plan and the review document say
so.

| Plan | Date | Task | Result |
|---|---|---|---|
| [TP-001](TP-001-self-hosting-dry-run.md) | 2026-08-02 | F-S4-07 | Pass, with two defects found and fixed; **the real-project run is still outstanding** |
