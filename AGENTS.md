# AGENTS.md

This repository is the source of the `mail` CLI — a read-only macOS tool for
searching and analyzing Apple Mail, built to be driven by scripts and LLM
agents.

- Build and install the binary: `make build` / `make install`.
- Install the agent skill (teaches agents how to drive `mail`):
  `make install-skills` (or `./mail install-skills`).
- Full behavior contract: `mail reference` (embedded) or
  `internal/reference/reference.md`.

Safety red lines for `mail`: always `--dry-run` before `mail trash`; never
trash without a filter or ROWID; never open `action` links automatically; the
tool makes no network requests.
