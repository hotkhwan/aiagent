# Assets — licensing

> ## Licensed: CC BY 4.0 (owner, 2026-11-27)
>
> The **code** in this repository is Apache-2.0. The **images** in
> `web/assets/` are © 2026 JOINANNY and licensed under
> [Creative Commons Attribution 4.0 International (CC BY 4.0)](https://creativecommons.org/licenses/by/4.0/):
> you may share and adapt them, also commercially, as long as you credit
> **"AI Agent World art by JOINANNY, CC BY 4.0"** and link the licence. See
> [LICENSE-ASSETS.md](../LICENSE-ASSETS.md).
>
> They were **generated with AI** (ChatGPT image generation, by OpenAI) and
> are labelled as such here, as OpenAI's terms require. Copyright in
> AI-generated images may be limited by law; the licence grants whatever
> rights JOINANNY holds, with no warranty.

## What is in here

`web/assets/` holds **234 files (about 12 MB)** — 225 PNG and 9 JPG — embedded into
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

The eight room glyphs are **not** in that count and are not files. They are SVG
path strings in `iconPaths` in `web/app.js`, so they can inherit the room's
colour (`docs/REDESIGN_SPEC.md` §2). Source: `roomIcons/iconPaths.json` in the
owner's `aiagent_art_rebuild_starter_20261107.zip`, added 2026-11-07. Same
provenance question as the rasters below — generator, account and terms are not
yet recorded.

## What is known

- **Generator:** ChatGPT image generation (OpenAI), on the owner's account.
  The owner supplied the files and recorded this on 2026-11-27.
- **Terms:** [OpenAI Terms of Use](https://openai.com/policies/row-terms-of-use/),
  effective 2026-01-01, which apply outside the EEA, Switzerland and the UK:
  the user owns the Output, and OpenAI assigns to the user its right, title
  and interest, if any, in the Output. The same terms forbid presenting Output
  as human-generated, which is why this page says plainly that it is not.
- **No third-party marks.** The agent providers are shown with neutral marks
  — `✻` and `>_` — not with Anthropic's or OpenAI's trademarks.
- **Contamination check:** all 234 files were looked at on contact sheets on
  2026-11-27, for logos, watermarks, signatures, and characters from someone
  else's work. None were found. That was a check at thumbnail size, not a
  legal clearance; look at new art at full size before adding it.

## Before adding or replacing art

1. **Record provenance** for each batch below: generator, account or plan,
   the terms on that date, and the date.
2. **Check for contamination** at full size: no logos, watermarks, signatures,
   or recognisable characters from another work.
3. **Only add art you may license under CC BY 4.0.** Art that cannot be
   licensed that way does not go in `web/assets/`.
4. **Fill in the table.** One row per batch, with its real source.

### Provenance register

| Files | Source / generator | Terms at time of generation | Chosen licence | Recorded by | Date |
|---|---|---|---|---|---|
| `web/assets/*` (234 files) | ChatGPT image generation (OpenAI), owner's account; supplied by the owner | OpenAI Terms of Use, effective 2026-01-01: the user owns Output; OpenAI assigns its rights in Output to the user; Output may not be presented as human-made | CC BY 4.0 | owner (hotkhwan) | 2026-11-27 |
| Room glyphs (`iconPaths` in `web/app.js`, from `aiagent_art_rebuild_starter_20261107.zip`) | *owner to confirm: same ChatGPT source as the images?* | — | CC BY 4.0 once confirmed | — | — |

## Earlier decision: regenerate the set (owner, 2026-11-06) — superseded

On 2026-11-06 the owner chose to replace every image rather than reconstruct
provenance. On 2026-11-27 the owner recorded the provenance instead (above),
so the current set stays and is licensed CC BY 4.0.
[ASSET_SPEC.md](ASSET_SPEC.md) still specifies every slot, file by file, for
any new or replacement art.

## If a batch cannot be cleared

The code stands on its own. Replace the images rather than hold the project
back: the serving path is a plain allowlist over an embedded directory
(`web/embed.go`), so a new set with the same file names drops in with no code
change. CC0 or CC BY sets exist for exactly this kind of isometric scenery.

## Trademarks

"Claude" is a trademark of Anthropic, "Codex" of OpenAI, "tmux" and other tool
names belong to their respective owners. This project is not affiliated with,
endorsed by, or sponsored by any of them. Do not add their logos; the neutral
marks already in the UI are the deliberate choice.
