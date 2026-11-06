# Props batch 02 — installed

Ten raw images, 2026-11-07, the same ten props as batch 01 redrawn after
`PROP_BRIEF.md` §2 was corrected. **Installed.**

Evidence: [props-batch02-rooms.jpg](props-batch02-rooms.jpg) — code, test and
library, the previous props on the left, these on the right, composed at the
real `roomDefs` positions over the real backgrounds by `tools/roomcompare`.

## What changed from batch 01

Painted, with a light direction and soft shading, so a prop sits in the room
instead of being pasted on it. The library shelves are warm wood under amber
rim light and read as bookcases in an amber room; batch 01's were black slabs.
The white desk is light enough for the white lab. This is the technique the
brief asked for.

## Preparation

The art arrived as raw generator output: 1024x1536 and similar, the object
floating in the middle of a wide transparent margin, with the subject at
alpha 252 rather than 255. `tools/propprep` trimmed each to its alpha, scaled
it to the width the room draws it at (times two for a retina iPad, read from
`roomDefs` — not typed in), raised the near-opaque interior to 255 and wrote it
under the name the code looks up.

Nothing was upscaled: every source was larger than the room needs.

| | |
|---|---|
| Alpha | real — 27% to 53% of each canvas is fully transparent, no matte |
| Sizes after prep | 180 to 788 wide, each exactly `drawn x 1.875 x 2` |
| Suite | `go test ./...` green |

## Size, and the quantiser

As 32-bit RGBA the ten came to **3.8 MB**, against 101 KB for the pastel set
they replace. There are 39 props; at that rate the binary gains 15 MB and an
iPad pays for it on every room.

`propprep` now builds a 256-colour palette per file by median cut and lets
`image/draw`'s Floyd-Steinberg do the matching. A PNG palette carries alpha
per entry, so the soft edge survives. **3.8 MB → 1.05 MB**, and at the size
the desk is actually drawn the two are indistinguishable — the dither is
invisible and the gradients hold. Checked by eye at 600px before installing.

## Still open

1. **`plant-big` does not read as a plant.** It is painted now and consistent
   with the rest, but it is still the pink and teal fan on a stalk from batch
   01, not foliage. It is drawn at 74px in three rooms, so it is not loud —
   but it is the one item that did not get fixed. Worth a redraw.
2. **The props lost their contents**, as in batch 01. `desk-blue2` is a desk
   and two monitors; the one it replaces also had a chair, a small robot, a
   plant and books. `shelf-big` lost its lamp, framed picture, boxes and vine;
   `shelf-tall` lost the robot sitting in it. The rooms read emptier, which is
   visible in the comparison. Brief §2.3 asks for this and it did not happen —
   worth raising before the next 29 props are drawn, not after.
3. **The dark props are heavy in the white test lab.** `filing-blue`,
   `printer2` and `desk-blue2` are darker than the room wants. Acceptable —
   they read as equipment — but a lighter cut would be better.

29 props remain. See `docs/PROP_BRIEF.md` for the list and sizes.
