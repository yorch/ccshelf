# Project: name, license and open source

## Open source (R4)

**R4: open source.** No org-specific names, URLs or assumptions in code, schemas or defaults. Everything org-specific lives in config and in the org's marketplace repo. The project needs:
- a license
- contribution docs
- a project and command name (decided: `ccshelf`, with the caveats noted under "Name" below)
- docs that don't depend on internal infrastructure.

This reinforces R2 (GitHub.com, GHE Cloud and GHE Server all supported) and R1 (all three operating systems).

## License (decided 2026-10-06)
**MIT** for the tool repo (code and docs). The `LICENSE` file exists at the repo root (copyright holder Jorge Barnaby, 2026). Still to do before the first public commit: confirm that the employer allows open-sourcing this, and that the employer has no IP claim on it. The org data repo (the org's profiles and catalog data) is the org's own, and this license does not cover it.

## Name (decided 2026-10-06): `ccshelf`
The project and the command are both called **`ccshelf`**: kits (profiles and bundles) on a shelf (the catalog), with the `cc` prefix signaling the Claude Code ecosystem. The word "profile" stays the name of the concept in the docs (a `ccshelf` profile).

History: we dropped the working name `claude-profile` because three existing tools share it and "Claude" in a name carries brand risk. We chose `ccprofiles` next and then replaced it, for these reasons:
- Its GitHub user is taken.
- Near-identical names exist (`ccprofile`, `cc-profiles`, `ccprof`, some doing similar things).
- It names only the launcher half of the project.

What we checked for `ccshelf` (2026-10-06, by command, nothing registered):
- **Free:**
  - No GitHub user or org of that name, and no repository named or described `ccshelf`.
  - npm, PyPI, crates.io, Homebrew (formula and cask) and the Go proxy return nothing.
  - A second agent also found Scoop and Arch/AUR clear.
  - No local binary.
  - `ccshelf.dev`, `.com` and `.app` are unregistered per RDAP, and `.io` and `.sh` per whois.
- **Brand:** `cc` is a common abbreviation in community tools but does not remove the affiliation question (Anthropic's trademark guidelines forbid implying sponsorship or affiliation). The tagline should say what the tool is and that it is unofficial: for example "ccshelf: profiles and a plugin catalog for Claude Code. Unofficial; not affiliated with Anthropic." A proper trademark search (classes 9 and 42) is still advised, and so is registering the GitHub org early.
- **Not checked:** winget, pkg.go.dev, trademark databases, and a web search for non-software uses of the name (for example a company called Ccshelf).

Consequences applied across the notes:
- command `ccshelf` (users can alias it)
- config in `~/.config/ccshelf/`
- cache in `~/.cache/ccshelf/`
- per-project directory `.ccshelf/`
- org config file `ccshelf.toml`
- Action path `<owner>/ccshelf/action`
- env var `CCSHELF_PROFILE`.

The local folder name `claude-profile` is only the current checkout directory.
