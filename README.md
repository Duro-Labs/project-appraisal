# project-appraisal

Measures where AI spend goes. `appraisal sweep` reads every local Claude Code, Codex and Hermes session, links sessions that are the same piece of work, and totals their tokens per group. `appraisal serve` opens a local page to merge, split, nest and rename groups; totals recalculate on every edit.

The **Memory**, **Rules**, **Skills** and **Hooks** tabs inventory known agent files (skills are `SKILL.md` entries under global and workspace `skills/`, `.claude/skills`, `.agents/skills` and `.codex/skills`) from Claude, Codex, Hermes, shared agent configuration, and workspaces seen in session history. Each searches names, paths, and file contents. Files can be edited or deleted after a revision check; generated rules and Hermes YAML config are read-only, and hook configuration files cannot be deleted from the tab. Changes to JSON configuration must remain valid JSON. The inventory is read live from local files and is not uploaded.

    go build -o appraisal ./cmd/appraisal
    ./appraisal sweep              # incremental; JEV judges uncertain pairs (budget --max-judgments, default 500)
    ./appraisal sweep --no-judge   # rules only, no network
    ./appraisal status
    ./appraisal serve              # http://127.0.0.1:7790

Reads `~/.claude/projects` plus any archived copies unpacked to `<data>/archives/claude/<name>/projects` (a session already present live is never replaced by its archived copy), `$CODEX_HOME` (default `~/.codex`) and every Hermes profile `state.db` under `$HERMES_HOME` (default `$XDG_DATA_HOME/hermes` if present, else `~/.hermes`), all read-only. Writes only `$XDG_DATA_HOME/project-appraisal/appraisal.db` (default `~/.local/share/project-appraisal/`).

Optional Obsidian Vault connector following the [hoard](https://github.com/Duro-Labs/hoard) layout: it groups non-code sessions by Vault subject folder and links sessions that wrote the same Vault note. The root comes from `--vault <root>`, `APPRAISAL_VAULT=<root>`, or hoard's `HOARD_VAULT_ROOT`; off by default. Only session cards (prompt excerpts, file names, branch) are sent to TypeSafe JEV; the key comes from `TYPESAFE_API_KEY` or Hermes's `.env`.

Token counts are usage as recorded by each tool, deduplicated per API request. They are not a bill: cost (phase 2) is applied later per model and plan.
