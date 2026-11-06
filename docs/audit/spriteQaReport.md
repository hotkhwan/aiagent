# Sprite QA — recovery batch, 2026-11-06

36 named sheets delivered and **installed**. Measured with `tools/assetaudit`;
the batch's own README asks not to claim the animations are complete, and this
does not.

## What is right

- **All 36 canvases are exact**: 768x128 for `walkL`/`walkR`, 512x128 for the
  rest. Real RGBA alpha on every one.
- **10 of 12 walks genuinely stride.**

The walk check had to be rewritten to get that number. Whole-silhouette width
is the obvious measure and it is wrong: a character carrying a laptop has an
arm that holds the outline steady while the legs move underneath. Measured on
`ch-dev-walkR` the whole body varies 2.1% while the legs vary 37.5%. Looking
only at the bottom third of each frame answers the question actually asked.

## What is weak, and will be visible

### Two walks do not walk

`ch-research-walkL` and `ch-research-walkR`: leg spread 0.033 against
0.17–0.41 for the ten that work. Confirmed by eye — front-facing, six nearly
identical standing poses. The researcher will slide between rooms.

### Seven files are byte-identical copies of a sibling

- `ch-deploy-cheer` = `ch-deploy-rest` = `ch-deploy-think` = `ch-deploy-work`
- `ch-infra-cheer` = `ch-infra-rest` = `ch-infra-work`

Infra looks the same whether it is working, resting or celebrating; Deploy the
same across four states. Only **31 distinct images fill 36 slots**. The
batch's README calls this "pose reuse" and flags it `requiresArtReview`; it is
recorded here as what it measurably is.

### Six sheets repeat a frame internally

`ch-designer-rest`, `ch-dev-rest`, `ch-dev-think`, `ch-dev-work`, `ch-research-rest`, `ch-tester-rest`

A pair of frames identical in both shape and colour. The loop stutters rather
than cycles.

## Every sheet, measured

| File | Size | Frames | min shape Δ | min colour Δ | leg spread |
|---|---|---:|---:|---:|---:|
| `ch-deploy-cheer` | 512x128 | 4 | 0.0281 | 0.06 | — |
| `ch-deploy-rest` | 512x128 | 4 | 0.0281 | 0.06 | — |
| `ch-deploy-think` | 512x128 | 4 | 0.0281 | 0.06 | — |
| `ch-deploy-walkL` | 768x128 | 6 | 0.0244 | 0.0371 | 0.303 |
| `ch-deploy-walkR` | 768x128 | 6 | 0.0246 | 0.0395 | 0.303 |
| `ch-deploy-work` | 512x128 | 4 | 0.0281 | 0.06 | — |
| `ch-designer-cheer` | 512x128 | 4 | 0.0702 | 0.0907 | — |
| `ch-designer-rest` | 512x128 | 4 | 0.0062 | 0.0111 | — |
| `ch-designer-think` | 512x128 | 4 | 0.1089 | 0.1 | — |
| `ch-designer-walkL` | 768x128 | 6 | 0.0202 | 0.0174 | 0.171 |
| `ch-designer-walkR` | 768x128 | 6 | 0.02 | 0.0179 | 0.171 |
| `ch-designer-work` | 512x128 | 4 | 0.0277 | 0.0256 | — |
| `ch-dev-cheer` | 512x128 | 4 | 0.0225 | 0.0295 | — |
| `ch-dev-rest` | 512x128 | 4 | 0.0045 | 0.0096 | — |
| `ch-dev-think` | 512x128 | 4 | 0.0078 | 0.007 | — |
| `ch-dev-walkL` | 768x128 | 6 | 0.0356 | 0.0318 | 0.375 |
| `ch-dev-walkR` | 768x128 | 6 | 0.0351 | 0.0248 | 0.375 |
| `ch-dev-work` | 512x128 | 4 | 0.0038 | 0.0097 | — |
| `ch-infra-cheer` | 512x128 | 4 | 0.0818 | 0.0576 | — |
| `ch-infra-rest` | 512x128 | 4 | 0.0818 | 0.0576 | — |
| `ch-infra-think` | 512x128 | 4 | 0.0213 | 0.0235 | — |
| `ch-infra-walkL` | 768x128 | 6 | 0.0221 | 0.0327 | 0.292 |
| `ch-infra-walkR` | 768x128 | 6 | 0.0177 | 0.0304 | 0.292 |
| `ch-infra-work` | 512x128 | 4 | 0.0818 | 0.0576 | — |
| `ch-research-cheer` | 512x128 | 4 | 0.0335 | 0.0597 | — |
| `ch-research-rest` | 512x128 | 4 | 0.0042 | 0.0084 | — |
| `ch-research-think` | 512x128 | 4 | 0.0062 | 0.0127 | — |
| `ch-research-walkL` | 768x128 | 6 | 0.0207 | 0.0178 | 0.033 |
| `ch-research-walkR` | 768x128 | 6 | 0.0187 | 0.0195 | 0.033 |
| `ch-research-work` | 512x128 | 4 | 0.0094 | 0.0178 | — |
| `ch-tester-cheer` | 512x128 | 4 | 0.0861 | 0.0979 | — |
| `ch-tester-rest` | 512x128 | 4 | 0.0033 | 0.0073 | — |
| `ch-tester-think` | 512x128 | 4 | 0.0107 | 0.02 | — |
| `ch-tester-walkL` | 768x128 | 6 | 0.0135 | 0.0224 | 0.413 |
| `ch-tester-walkR` | 768x128 | 6 | 0.0131 | 0.0237 | 0.413 |
| `ch-tester-work` | 512x128 | 4 | 0.0833 | 0.0669 | — |

Shape and colour distances below ~0.012 on both counts mean a repeated frame.
Leg spread applies to walks only; under 0.10 means the legs are not moving.

## Why they were installed anyway

The village had pastel sprites from the previous art direction standing in the
new cyberpunk rooms. These are coherent with the rooms and correct in every
structural respect. The weaknesses above are "less lively than intended", not
"renders wrong".

What to re-request: `ch-research-walkL/walkR` with an actual stride, and
distinct poses for the seven duplicated infra/deploy states.

---

# Update, 2026-11-07: the seven re-renders

All seven weak slots were replaced. Identified by looking at them — the files
arrived unnamed, so each was matched to a slot by character and pose: human in
a yellow hard hat for infra, visored robot for deploy, silver hair and a lab
coat for research; tablet and server hologram for `work`, question mark and
lightbulb for `think`, relaxed stance for `rest`; and the walks by which way
the character faces.

| Slot | What it fixes |
|---|---|
| `ch-research-walkL` / `walkR` | a real side-profile stride, replacing six near-identical front-facing poses |
| `ch-infra-rest`, `ch-infra-work` | distinct poses; infra no longer looks the same working, resting and celebrating |
| `ch-deploy-rest`, `think`, `work` | distinct poses; deploy no longer identical across four states |

**All 36 sheets are now distinct images.** Previously 31 images filled 36
slots; no two files share a hash.

## On the leg-movement metric: it is a hint, not a verdict

Three versions of this check were written and **all three gave a wrong answer
on a real sheet**, each caught by looking at the picture afterwards:

1. **Whole-silhouette width.** A character carrying a laptop has an arm that
   holds the outline steady while the legs stride. A good walk scored 2%.
2. **Bottom-third bounding-box width.** A character in a long lab coat keeps a
   constant hem width while the legs move inside it. A good walk scored 1.4%.
3. **Bottom-third shape comparison.** Scores the *old* static research walk at
   0.237 — the same range as walks that genuinely stride.

The numbers are still recorded because they catch the obvious cases, and a
frame that is byte-identical to its neighbour is caught reliably by the shape
AND colour test. But **walk quality in this project is confirmed by opening
the converted sheet and watching the legs**, and every sheet called good above
was looked at.

---

# Update, 2026-11-07 (second batch): NOT installed

Five sheets arrived for the five slots that still repeat a frame internally —
`ch-designer-rest`, `ch-dev-rest`, `ch-dev-think`, `ch-dev-work`,
`ch-tester-rest`. They are **not installed**, because they are a redesign of
the characters rather than a repair of those five animations.

## What was measured

Mean colour of the opaque pixels in the head region of frame 1, against the
character already in the tree:

| Slot | Installed | New | Distance |
|---|---|---|---:|
| `dev` | RGB(191, 89, 145) — pink | RGB(28, 92, 171) — blue | **164** |
| `designer` | RGB(129, 79, 147) | RGB(110, 71, 106) | 47 |
| `tester` | RGB(70, 89, 81) | RGB(100, 85, 89) | 31 |

Three of the five sheets are a **blue-haired** character. The developer in the
tree is pink-haired, in all six of its existing animations. Installing these
would give a developer who is pink-haired while walking and celebrating and
blue-haired while working, thinking and resting.

The other two are closer but still different people on screen: the installed
designer has solid purple hair and carries a tablet, the new one has dark hair
with pink tips and carries a palette and swatches; the installed tester glows
teal and holds a tablet, the new one glows blue and holds a clipboard.

## Why that is worse than the bug it fixes

The defect being repaired is a repeated frame — a loop that stutters. The
defect that would be introduced is a character whose identity changes with its
mood. The first is a small animation flaw; the second reads as two different
agents and is noticed immediately.

Structurally the sheets are fine: four distinct poses each, no repeated frames,
chroma green, and they convert cleanly. Nothing is wrong with them as art.

## The decision this needs

Two coherent ways forward, and they should not be mixed:

1. **Keep the cast as installed** and re-request these five against the
   existing designs — pink-haired dev, purple-haired designer with a tablet,
   teal tester. The reference is in the tree: `web/assets/ch-<role>-cheer.png`.
2. **Accept the redesign** and re-request **all 36** in it. Half a cast in each
   style is the one outcome to avoid.

The five files are untouched in `~/Downloads` either way.
