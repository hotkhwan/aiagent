# Assets — licensing status

> ## ⚠ UNRESOLVED: the images in `web/assets/` are not yet licensed for redistribution.
>
> The **code** in this repository is Apache-2.0. The **images are not covered
> by it**. Until the owner records their provenance and chooses a licence
> below, treat every file in `web/assets/` as **all rights reserved**: do not
> redistribute it, do not vendor it into another project, and do not publish a
> fork that includes it.
>
> This is a blocker for calling the repository properly open source. It is
> recorded here rather than quietly ignored.

## What is in here

`web/assets/` holds **230 files (4.6 MB)** — 222 PNG and 8 JPG — embedded into
the binary and served through a name allowlist. By subject:

| Prefix | Count | What it is |
|---|---:|---|
| `cx-*` | 42 | character expressions and poses |
| `ch-*` | 36 | characters (agent robots, NPCs) |
| `sky-*` | 8 | sky layers for the time of day |
| `bg-*`, `plq-*` | 16 | room backgrounds and plaques |
| `house*`, `wall`, `door`, `tile` | ~19 | building exteriors and surfaces |
| `desk`, `sofa`, `rug`, `shelf`, `plant`, `bean`, `post` | ~27 | furniture and props |
| `arr-*`, `scr-*`, `fx-*` | ~18 | arrows, screens, effects |
| other | remainder | icons and one-off scenery |

## What is known

- They were supplied by the project owner and were **generated with AI image
  tools**. Which tools, under which terms of service, and from which prompts
  or reference images, is **not recorded**.
- They contain **no third-party logos**. The agent providers are shown with
  neutral marks — `✻` and `>_` — not with Anthropic's or OpenAI's trademarks.
- As far as the maintainer is aware they contain no third-party copyrighted
  characters or stock imagery, but **this has not been verified**.

## What has to happen before a public release

1. **Establish provenance.** For each batch: which generator, which account,
   under which terms, and on what date. Some providers grant the user
   commercial rights to outputs; some do not; some depend on the plan. The
   answer decides everything else.
2. **Check for contamination.** AI output can reproduce a trademarked
   character or a watermark. Review the set by eye before publishing it.
3. **Choose a licence** and write it into the table below — CC BY 4.0 is the
   usual choice for art alongside Apache-2.0 code, but it is only available if
   step 1 says the owner may sublicense.
4. **Fill in the table.** One row per batch, with its real source.

### Provenance register — to be completed by the owner

| Files | Source / generator | Terms at time of generation | Chosen licence | Verified by | Date |
|---|---|---|---|---|---|
| `web/assets/*` | *unrecorded — AI-generated, supplied by the owner* | **unknown** | **undecided** | — | — |

## If the licence cannot be cleared

The code stands on its own. Replace the images rather than hold the project
back: the serving path is a plain allowlist over an embedded directory
(`web/embed.go`), so a new set with the same file names drops in with no code
change. CC0 or CC BY sets exist for exactly this kind of isometric scenery.

## Trademarks

"Claude" is a trademark of Anthropic, "Codex" of OpenAI, "tmux" and other tool
names belong to their respective owners. This project is not affiliated with,
endorsed by, or sponsored by any of them. Do not add their logos; the neutral
marks already in the UI are the deliberate choice.
