# Routing eval protocol (Phase 0.1, O-05)

Status: **protocol fixed 2026-10-06; runs pending the org's plugin set.** The success criterion in section 5 is written before any run. Changing it after seeing data invalidates the eval; record any change as a new dated version of this file, with the reason, and rerun.

Labels: {V} verified (docs, `gh`, or ran it), {R} reported, {U} unverified. The stream-json event shape below is {V} for the `system/init` event (see [stage0.md](stage0.md)) and {U} for the `tool_use` and `usage` fields, until the pilot in step P0 confirms them.

## 1. Question and hypothesis

Question: is the launcher worth building as a product?

Hypothesis H1: with the same model, the same prompts and the same machine, a session whose active plugin set is limited to a role profile selects the intended skill more often on the first try, and activates a wrong skill less often, than a session with the full installed set. The reason to expect it: the skill listing is capped (about 1% of the context window) so descriptions of rarely used skills can be dropped, and many overlapping descriptions compete {V per landscape.md}. Token savings are not the hypothesis: Stage 0 measured about 9% when the whole user layer was dropped {V}.

Null H0: no measurable difference, or a regression on explicit prompts.

If H1 is rejected, the launcher is documented as an alias recipe (`dry-run` prints the exact command) and is not shipped as a product, as the roadmap says.

## 2. Arms

| Arm | What runs | Active set |
|---|---|---|
| `control` | plain `claude -p ...` | everything installed and enabled on the eval machine |
| `profile` | the same command through the launcher with one profile, or an equivalent generated `--settings` file (default-deny `enabledPlugins`, `skillOverrides`, MCP denies) | only the profile's closure |

Rules:
- One machine, one Claude Code version (record `claude --version`), one model (`--model` fixed, default a small fast model; repeat the whole eval once on the model people use daily if budget allows), same working directory for all runs: an empty scratch directory with no project settings or `CLAUDE.md`.
- The profile must contain every skill that a prompt expects. Prompts whose expected skill is outside the profile are **out-of-scope** and are analyzed separately (they measure the cost of a too-narrow profile, not the benefit).
- Run order is shuffled and the arms are interleaved (the harness does this with a fixed seed), so time drift and rate limits hit both arms alike.
- The harness only reads. It never installs, enables or disables a plugin, and never edits `~/.claude`. Because a run uses the person's real config as the control, a person or the org's CI runs it, not an agent session.

## 3. Dataset

### Tiers
| Tier | Prompt type | Expected outcome | Minimum prompts |
|---|---|---|---|
| 1 | Explicit: the task clearly matches one skill and a person would name it | that skill fires first | 30 |
| 2 | Ambiguous or overlapping: two or more installed skills could plausibly apply; the intended one is the best fit | an acceptable skill fires first (an explicit list, set before the run) | 40 |
| 3 | No skill needed: ordinary coding or Q&A | no skill fires | 30 |

Tier 2 carries the hypothesis, so it gets the most prompts. At 40 prompts and 5 repetitions the paired confidence interval on a +10 point effect is around plus or minus 9 points if per-prompt differences have a standard deviation near 0.3 (my estimate, {U}); a smaller set cannot support the criterion.

### How to build it without leaking org data
- The prompt file lives in the **org data repo**, never in this public repo. This repo holds only the format, the harness and a fictional example (`routing-eval/prompts.example.jsonl`).
- Derive prompts from real work: anonymized, paraphrased past session openings and ticket titles, written by people who did **not** read the plugin descriptions (to avoid copying their wording, which would inflate routing). Do not have a model generate prompts from the descriptions.
- For tier 2, list overlapping plugin pairs from the catalog (for example two review skills, or two deploy skills) and write a prompt for each side of the pair; two reviewers independently name the best skill, and a prompt is kept only if they agree. Disagreement means the case is too ambiguous to score.
- Remove names of customers, hosts, people, repositories and internal URLs. A prompt must make sense to a stranger.
- **Freeze before running:** commit the prompt file and the expected-skill lists in the org data repo, record its SHA-256 (the harness prints it and stores it in every run record) and the commit hash in the results. Prompts are not edited after the first run.
- Split off about a quarter of tier 2 as a held-out set that is not used while tuning the profile; the verdict is read on the held-out set only if any profile tuning happened after seeing results.

### Prompt file format
JSON lines, one object per prompt: `id`, `tier` (1, 2 or 3), `prompt`, `expect` (`{"skills": [names]}` or `{"none": true}`). A name is a skill name or `plugin:<plugin>` to accept any skill of that plugin. The harness rejects unknown keys and duplicate ids. See `routing-eval/prompts.example.jsonl`.

## 4. Method and metrics

### Run command (one run)
```
claude -p "<prompt on stdin>" --output-format stream-json --verbose --max-turns 1 --model <model>
```
The harness runs it in an empty scratch directory with the prompt piped to stdin (never on argv). `--max-turns 1` ends the run after the first model turn, so the first `tool_use` block, if any, is the choice under test and no tool actually runs to completion. In `-p` mode a skill call may be denied by the permission mode; the `tool_use` event is emitted before that, so the choice is still observed {U}. The `profile` arm is the same command with the launcher or `--settings`; for example `ccshelf run <profile> -- -p ... --output-format stream-json --verbose --max-turns 1` (mockup: confirm the launcher's argument forwarding in the pilot).

### What is read from the stream
- `system/init` event {V}: counts of `plugins`, `skills`, `slash_commands`, `agents`, `tools`, `mcp_servers`. Recorded per run, to prove the arms differ as intended (a profile arm whose init counts equal the control's invalidates the pair).
- The first `assistant` event's `message.content` blocks of type `tool_use` {U}: the name `Skill` and `input.skill` give the chosen skill; any other first tool name is recorded too. Skill names may carry a `plugin:` prefix, which the scorer handles.
- Context size: the first `assistant` event's `message.usage` {U}, summed input, cache-creation and cache-read tokens.
- The final `result` event {V as in Stage 0 tests}: error flag, cost, turns. Errored or timed-out runs are excluded and counted in the report.

### Step P0, pilot (not part of the data)
Before the frozen run, do 3 prompts in each arm by hand with `--output-format stream-json --verbose`, and confirm: the init counts differ between arms; a `tool_use` with name `Skill` appears for a prompt that should trigger one; `usage` is present. If the field names differ, fix `parse_stream` in the harness, record the change here and discard the pilot runs. This is the only allowed change after the freeze, and it concerns parsing only.

### Metrics (per run, then per prompt as the mean over repetitions)
| Metric | Definition |
|---|---|
| Correct-first-try | tier 1 and 2: the first skill used matches `expect.skills`; tier 3: no skill used |
| Wrong activation | a skill was used first and it is not acceptable (tier 3: any skill) |
| Miss | tier 1 and 2: no skill used at all |
| Context tokens | first-turn input tokens (secondary, descriptive only) |

Only the first skill choice counts. Later turns are not run.

## 5. Success criterion (fixed)

The launcher is worth building as a product only if all of these hold on the in-scope prompts, using the paired difference `profile minus control` of per-prompt means and a 95% percentile bootstrap confidence interval over prompts (10,000 resamples, fixed seed):

1. **Benefit** (either one):
   - tier 2 correct-first-try rises by at least **10 percentage points** and the interval's lower bound is above 0; or
   - wrong activation falls by at least **5 percentage points** on tier 2 or tier 3 and the interval's upper bound is below 0.
2. **No harm:** tier 1 correct-first-try does not drop by more than **5 percentage points** (interval lower bound at least -0.05).
3. **Sanity:** init counts differ between the arms in the intended direction in at least 95% of runs, and at least 95% of runs completed without error.

If 1 holds but 2 or 3 fails, the result is "inconclusive": fix the cause (a too-narrow profile, a harness fault) and rerun on a fresh prompt set; do not reuse the data. If 1 fails, document the alias recipe and do not build the product. Context-token reduction and latency are reported but never decide the outcome.

Machine check: `run_eval.py analyze` prints these three tests and the verdict. Thresholds are flags with these defaults; do not change them after the freeze.

## 6. Run counts, statistics and stop rules

- **Repetitions:** 5 per prompt per arm (the model is not deterministic). With 100 prompts, 2 arms and 5 repetitions that is 1,000 runs; the harness refuses more than `--max-runs` (default 2000).
- **Unit of analysis:** the prompt, not the run (runs of one prompt are not independent). Differences are paired by prompt.
- **Tiers are reported separately**, plus a pooled figure for context; the criterion uses tiers separately.
- **Out-of-scope prompts** are reported as a separate table of the cost of narrowing.
- **Multiple comparisons:** the criterion names its two benefit tests in advance, so no correction is applied, but any other metric or subgroup is exploratory and labeled so.
- **Futility stop:** after 15 tier 2 prompts and 3 repetitions in both arms, if the tier 2 interval's upper bound for correct-first-try is below +5 points and for wrong activation the lower bound is above -2 points, stop and record "no benefit".
- **Harm stop:** if tier 1 correct-first-try drops by more than 10 points with the interval's upper bound below 0 at any interim look, stop and investigate the profile.
- **Budget stop:** the run cap above, plus a spend limit set by the org before the run. A rate-limit or outage stops the run; resume from `runs.jsonl` is not automatic (rerun the missing cells, keeping the same seed and freeze).
- **Looks:** at most the two named interim looks and the final analysis; no peeking beyond that.

## 7. Reporting

Write the result in the org data repo (it names real plugins), and put only the aggregate, anonymized numbers in `docs/research/` here: counts per tier, effect sizes and intervals, `claude --version`, model, dates, harness commit, prompt-file hash. Update O-05 and the Phase 0.1 checkbox only when the final analysis is done.

## 8. Threats to validity

- Small number of prompts from one org: the result generalizes to that org's plugin set only.
- The control is one particular machine's installed set; an org with fewer installed plugins sees a smaller effect.
- The model changes over time; record the version and rerun at each Claude Code minor release if the decision is close.
- First-turn selection is not task success: a wrong first skill may still lead to a good answer. The metric measures routing, which is the claim.
- A profile tuned on the same prompts overfits; use the held-out split.

## Files
- `routing-eval/run_eval.py`: harness template (Python 3, standard library only; `run` prints a plan unless `--execute` is given; `analyze` applies section 5). Parsing was checked only against a synthetic file; it has not been run against a real Claude Code stream.
- `routing-eval/prompts.example.jsonl`: fictional format example.
