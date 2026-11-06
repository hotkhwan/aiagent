# Asset QA report — candidate art in ~/Downloads

Audit only. **Nothing in `web/assets` was changed, and no file was renamed.**
Measurements come from the pixels (`tools/assetaudit`), not from filenames.

- Inventory: [assetInventory.json](assetInventory.json) — SHA-256, size,
  dimensions, mtime and frame analysis for every file
- Contact sheets: [all 87](contact-sheet.png) · [rooms](contact-rooms.png) ·
  [sheets](contact-sheets.png). Cells are numbered; the number is the `index`
  in the inventory, which carries the filename and mtime.

## What is in the folder

87 images. **49 are the 2026-11-06 batch**; the other 38 predate it and are
LittleDays or unrelated art that happens to share the folder. They are marked
`notThisProject` and were not considered further.

| Status | Files | Meaning |
|---|---:|---|
| `needsReview` | 32 | sound, but which asset number it is has not been established from the pixels |
| `needsFix` | 17 | measurably wrong against the spec |
| `notThisProject` | 38 | older art in the same folder |
| `ready` | 0 | nothing is ready to ship as-is |

**Nothing is `ready`.** Every candidate needs at least a resample or a crop,
and none has been through a visual pass against the room it belongs to.

## Assets 1–10 — rooms and two skies: identified, resample only

Ten 1586x992 images, not chroma-key, in the exact order of the spec list.
Identified by content, each unmistakable: bookshelves for Library, a robotic
arm and vials for Test, server racks for Ops, hazard stripes and a launch pad
for Deploy, green doors for Approval, plants and warm light for Lounge. The
centres are clear, which is what the walking lane needs.

| Asset | Name | File index | Delivered | Spec | Action |
|---|---|---:|---|---|---|
| 1–8 | `bg-plan` … `bg-lounge` | 39–46 | 1586x992 | 1600x1000 | resample |
| 9–10 | `sky-plan`, `sky-library` | 47–48 | 1586x992 | 800x500 | resample |

Aspect is 1.599 against the spec's 1.600, so the resample is lossless in
practice. **This is the one group that is close to usable.**

## Assets 21–56 — characters: the real problem

36 sheets are specified. **18 chroma-key sheets were delivered**, and of those
**7 fail on their own pixels**:

| # | File | Size | Frames | min shape Δ | min colour Δ | silhouette spread | Status |
|---|---|---|---:|---:|---:|---:|---|
| 50 | `ChatGPT Image Nov 6, 2026, 04_15_36 PM-2.png` | 2508x627 | 4 | 0.0066 | 0.0091 | 0.032 | needsFix |
| 51 | `ChatGPT Image Nov 6, 2026, 04_15_37 PM-3.png` | 2508x627 | 4 | 0.004 | 0.0046 | 0.011 | needsFix |
| 53 | `ChatGPT Image Nov 6, 2026, 04_15_40 PM-5.png` | 2508x627 | 4 | 0.0041 | 0.0088 | 0.048 | needsFix |
| 57 | `ChatGPT Image Nov 6, 2026, 04_28_04 PM-1.png` | 3072x512 | 6 | 0.0164 | 0.0161 | 0.004 | needsFix |
| 58 | `ChatGPT Image Nov 6, 2026, 04_28_05 PM-2.png` | 3072x512 | 6 | 0.0197 | 0.0179 | 0.015 | needsFix |
| 61 | `ChatGPT Image Nov 6, 2026, 04_28_09 PM-5.png` | 2508x627 | 4 | 0.003 | 0.0088 | 0.12 | needsFix |
| 63 | `ChatGPT Image Nov 6, 2026, 04_28_11 PM-7.png` | 3072x512 | 6 | 0.0178 | 0.0215 | 0.011 | needsFix |

Two distinct faults:

- **Repeated frames** (4 files): a pair of frames is identical in both shape
  and colour. Played back, nothing moves.
- **A walk that does not walk** (3 files): six frames, but the silhouette
  width varies by under 1.5%. Verified by eye after conversion — the character
  faces front in all six frames with no leg swing. It would slide across the
  floor. Compare index 54, a genuine walk at 6.1% spread with a clear stride.

The other 11 are structurally sound:

| # | File | Size | Frames | min shape Δ | min colour Δ | silhouette spread | Status |
|---|---|---|---:|---:|---:|---:|---|
| 49 | `ChatGPT Image Nov 6, 2026, 04_15_34 PM-1.png` | 2508x627 | 4 | 0.0039 | 0.012 | 0.08 | needsReview |
| 52 | `ChatGPT Image Nov 6, 2026, 04_15_38 PM-4.png` | 2508x627 | 4 | 0.0198 | 0.027 | 0.103 | needsReview |
| 54 | `ChatGPT Image Nov 6, 2026, 04_15_41 PM-6.png` | 3072x512 | 6 | 0.0481 | 0.0267 | 0.061 | needsReview |
| 55 | `ChatGPT Image Nov 6, 2026, 04_15_42 PM-7.png` | 2508x627 | 4 | 0.0051 | 0.0156 | 0.081 | needsReview |
| 56 | `ChatGPT Image Nov 6, 2026, 04_15_43 PM-8.png` | 2508x627 | 4 | 0.0326 | 0.0596 | 0.139 | needsReview |
| 59 | `ChatGPT Image Nov 6, 2026, 04_28_07 PM-3.png` | 2508x627 | 4 | 0.0835 | 0.0687 | 0.163 | needsReview |
| 60 | `ChatGPT Image Nov 6, 2026, 04_28_08 PM-4.png` | 2508x627 | 4 | 0.0105 | 0.0237 | 0.115 | needsReview |
| 62 | `ChatGPT Image Nov 6, 2026, 04_28_10 PM-6.png` | 2508x627 | 4 | 0.0835 | 0.0922 | 0.139 | needsReview |
| 64 | `ChatGPT Image Nov 6, 2026, 04_28_12 PM-8.png` | 3072x512 | 6 | 0.0107 | 0.0203 | 0.03 | needsReview |
| 65 | `ChatGPT Image Nov 6, 2026, 04_28_13 PM-9.png` | 2508x627 | 4 | 0.0757 | 0.0576 | 0.292 | needsReview |
| 66 | `ChatGPT Image Nov 6, 2026, 04_28_14 PM-10.png` | 2508x627 | 4 | 0.0229 | 0.0274 | 0.235 | needsReview |

They are `needsReview` rather than `ready` for one reason: **the pixels do not
say which of the six roles or which animation each sheet is.** Assigning them
by guesswork would put a character in the wrong room with nothing to catch it.

### Assets 41–50 are not delivered

Index 67 (`Chibi Agent Sprite Sheet Grid 41-50.png`, 1536x1024) is a **labelled
contact sheet**: ten preview panels captioned `ch-infra-rest.png`,
`ch-deploy-walkR.png` and so on, with frame counts. It is a specification of
what 41–50 should be, not the ten sheets themselves. Useful as a reference for
naming; unusable as art.

## Assets 57–70 — UI and props: present, crop map outstanding

Fourteen chroma sheets identified by content:

| Asset | Name | Index | Note |
|---|---|---:|---|
| 57 | `icon-sheet-normal` | 74 | 8 room icons, 4x2, the spec's order |
| 58 | `icon-sheet-active` | 75 | same 8, glowing |
| 59–60 | `plaque-sheet-A/B` | 77, 76 | blank plaques, no text — as asked |
| 61–62 | `ui-frame-sheet-A/B` | 79, 82 | large frames; chips, badges, speech |
| 63–65 | buttons: primary / secondary / icon-toggle | 78, 81, 80 | states present |
| 66–70 | props: plan, library, code, test, ops | 86, 84, 85, 83, 87 | one sheet per room |

They need a **crop map** — which rectangle is which item, in the spec's order —
before anything can be sliced. That mapping was promised with each batch and
is not in the folder.

> The icons are listed in the spec as images, but `iconSvg()` draws room icons
> as SVG paths. These sheets are the right *reference* for redrawing those
> eight paths; shipping them as PNG would lose the per-room colour and the CSS
> glow. See REDESIGN_SPEC.md §2.

## What is missing outright

| Assets | What | Why not usable |
|---|---|---|
| 11–16 | `sky-code` … `sky-lounge` | only in the zip as 500x235 crops; spec is 800x500 |
| 17–19 | house-agcmd / daemon / terminal | zip crops ~340x327 |
| 20 | `bg-entrance` | zip crop 454x329 |
| 41–50 | infra + deploy characters | reference contact sheet only (above) |
| 71–73 | props deploy / approval / lounge | zip crops 301x416 |
| 74–81 | workflow town buildings | zip crops 301x416 |
| 82 | decor sheet | zip crop 1115x720 |

The three zips carry their own README saying it plainly: *"concept crops from a
generated contact sheet, not independently rendered production assets …
Do not drop into web/assets or deploy."* That is correct and is respected here.

## What to ask for next, in order

1. **Re-render the 7 broken character sheets.** For the three walks, the brief
   has to say the legs must swing — a front-facing stand repeated six times
   passes every size check and fails the only thing a walk is for.
2. **The 18 character sheets that were never delivered**, including all of
   41–50.
3. **A filename or a caption on every sheet.** The 11 good sheets are blocked
   on nothing but knowing which role and animation they are. Index 67 shows
   the generator can caption its output; applied to the real sheets, this
   whole class of ambiguity disappears.
4. **Crop maps** for 57–70.
5. **Re-render 11–20 and 71–82 at full size**, not as contact-sheet crops.

## How to check a sheet yourself

```sh
go run ./tools/assetaudit -in ~/Downloads -out audit.json -sheet contact.png
go run ./tools/spritesheet -in <sheet>.png -frames 6 -out /tmp/check.png
```

Then open `/tmp/check.png` and watch the legs. The numbers narrow it down; the
last step is always a human looking at the picture.
