# project-appraisal

Measures where AI spend goes. `appraisal sweep` reads every local Claude Code, Codex and Hermes session, links sessions that are the same piece of work, and totals their tokens per group. `appraisal serve` opens a local page to merge, split, nest and rename groups; totals recalculate on every edit.

The editor can remove a session from Appraisal without changing its source. Removed sessions stay excluded on later sweeps. Its Delete action can also delete the source transcript: the JSONL file for Claude Code or Codex, or the Hermes session through `hermes sessions delete`. Hermes may delete delegated child sessions with it. The source deletion option is confirmed separately in the editor, and the Hermes CLI must be installed to use it.

    go build -o appraisal ./cmd/appraisal
    ./appraisal sweep              # incremental; JEV judges uncertain pairs (budget --max-judgments, default 500)
    ./appraisal sweep --no-judge   # rules only, no network
    ./appraisal status
    ./appraisal serve              # http://127.0.0.1:7790

Reads `~/.claude/projects`, `$CODEX_HOME` (default `~/.codex`) and every Hermes profile `state.db` under `$HERMES_HOME` (default `$XDG_DATA_HOME/hermes` if present, else `~/.hermes`), all read-only. Writes only `$XDG_DATA_HOME/project-appraisal/appraisal.db` (default `~/.local/share/project-appraisal/`).

Optional Obsidian Vault connector following the [hoard](https://github.com/Duro-Labs/hoard) layout: it groups non-code sessions by Vault subject folder and links sessions that wrote the same Vault note. The root comes from `--vault <root>`, `APPRAISAL_VAULT=<root>`, or hoard's `HOARD_VAULT_ROOT`; off by default. Only session cards (prompt excerpts, file names, branch) are sent to TypeSafe JEV; the key comes from `TYPESAFE_API_KEY` or Hermes's `.env`.

Token counts are usage as recorded by each tool, deduplicated per API request. They are not a bill: cost (phase 2) is applied later per model and plan.
