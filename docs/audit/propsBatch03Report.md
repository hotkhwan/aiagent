# Props batches 03 and 04, and Round A — audit

2026-11-07. Three deliveries covering the 29 props that were left after
batch 02. Nineteen installed, ten rejected.

| Delivery | Props | Outcome |
|---|---:|---|
| 10 images, 09:27 | globe, table-meet, plant-stand, books2, desk-boss2, calendar, reception2, sofa-yellow2, water, flowers-pot | **installed** |
| 9 images, 14:57 | armchair2, hammock2, bean-purple, signboard, sakura2, bench2, flowerbox3, lamppost, bush2b | **installed** |
| `aiagent_props_roundA_candidates.zip` | pillow-bunny, servers2, rug-pink2, rocket, bean-pink, bean-yellow, box-pallet, handtruck, rug-cloud, sofa-pink2 | **rejected** |

Evidence: [props-batch03-rooms.jpg](props-batch03-rooms.jpg) — plan, approval,
lounge and library, before and after, composed by `tools/roomcompare` at the
real `roomDefs` positions. [props-roundA-rejected.jpg](props-roundA-rejected.jpg)
— the six Round A files that were large enough, against what they would have
replaced.

## The nineteen

Individually rendered at 1086x1448 or 1448x1086, painted in the same technique
as batch 02. `tools/propprep` trimmed, scaled and quantised them; **nothing was
upscaled**, every source was larger than the room needs.

Per room, from the comparison:

- **library** is the clearest win. The ornate reading table with its lamp, the
  brass globe and the richer books belong in an amber lantern-lit room in a way
  the pale pine set never did.
- **approval**: the reception desk is green neon and matches the green room
  exactly. The sofa reads darker — olive rather than pastel yellow — but sits
  better against the planting.
- **lounge**: the deep red armchair and the cushioned hammock are a real step
  up in material.
- **plan**: the dark executive desk suits the blue room. The meeting table is
  warm wood, which reads better in the library than it does here.

### The content-loss problem again

`table-meet` arrived as a bare table. The one it replaces had four chairs, a
plant, a book and a mug around it. In the library it reads as a reading table
and the loss is invisible; in the plan room it is an empty table in a room that
also has a desk. Same note as batch 02 — `PROP_BRIEF.md` §2.3 asks for the
group each file contains, and it still is not happening.

Not a reason to hold the batch: nineteen props that match the rooms beat four
chairs.

## Round A — rejected, two reasons

The package is honest about itself: *"10 individual RGBA PNGs extracted from a
generated illustrated contact sheet. **Not production-ready.**"* Both of its
warnings turned out to be the blocking ones.

**1. Four are far too small.** Every file is about 350px wide, because they
were cut from one sheet:

| Prop | Have | Needs | Short by |
|---|---:|---:|---:|
| `rug-pink2` | 354 | 1200 | 3.4x |
| `sofa-pink2` | 349 | 1035 | 3.0x |
| `rug-cloud` | 355 | 938 | 2.6x |
| `servers2` | 355 | 735 | 2.1x |

The other six (`pillow-bunny`, `rocket`, `bean-pink`, `bean-yellow`,
`box-pallet`, `handtruck`) are small props and 350px clears their requirement.

**2. The six that fit carry debris from the next cell.** Look along the right
edge of each in the comparison image: `bean-yellow` has an orange sliver,
`box-pallet` a yellow strip, `handtruck` a piece of blue cloud, `rocket` a pink
fragment. These are the neighbouring objects on the source sheet, caught by the
cut. The README predicted it — *"Several images touch column bounds or show
halo debris"*.

They are also a different finish: glossy, high-chroma, closer to a mobile game
icon than to the painted props now in the rooms. `bean-pink` is hot magenta
with a star on it, where the lounge is rose and amber.

## What to ask for

The same ten, **rendered individually** the way the 09:27 and 14:57 batches
were — one object per generation, not ten cut out of one sheet. That is the
whole difference: those two batches needed no argument at all.

Sizes, from `roomDefs`:

| Prop | Source width needed |
|---|---:|
| `rug-pink2` | 1200 |
| `sofa-pink2` | 1035 |
| `rug-cloud` | 940 |
| `servers2` | 735 |
| `rocket` | 360 |
| `bean-pink`, `bean-yellow` | 315 |
| `box-pallet` | 280 |
| `handtruck` | 205 |
| `pillow-bunny` | 195 |

Raw output is fine — any canvas, transparent margin, any size above those
numbers. `tools/propprep` does the rest.

## State

**29 of 39 props are now in the new design.** The ten above are what is left,
and until they arrive the lounge mixes a new red armchair with the old pastel
pink sofa, which is visible.
