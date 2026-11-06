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
