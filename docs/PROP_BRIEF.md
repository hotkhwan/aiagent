# Prop brief — the furniture, and why the rooms still look old

> **Status, 2026-11-07.** Ten of the 39 are done and shipped — `plant-big`,
> `desk-blue2`, `desk-pink2`, `desk-white2`, `cactus`, `filing-blue`,
> `printer2`, `hydrangea`, `shelf-big`, `shelf-tall`. See
> [audit/propsBatch02Report.md](audit/propsBatch02Report.md), including the
> two things that still need fixing in them. **29 to go.**
>
> Raw art does not need to arrive trimmed or sized: `tools/propprep` does
> that, reading the widths out of `roomDefs`. What it cannot do is draw.

The character cast and the room plaques are in the new design. The **props are
not**: every desk, sofa, rug and plant in the eight rooms is still the pastel
set from 2026-11-05. In a navy neon room a cream desk and a yellow sofa read as
a different game, and that is what is visible on screen today.

They were never redelivered. `REMAINING_ASSET_BACKLOG.md` Priority 3 and 4 cover
them, and the only source material that exists is concept previews:

| Sheet | What it was meant to be | What was delivered |
|---|---|---|
| 66–70 | prop sheets for plan, library, code, test, ops | **nothing** |
| 71–73 | prop sheets for deploy, approval, lounge | 301x416 concept preview, ~8 props per sheet |
| 82 | town decor sheet | 1115x720 concept |

A 301x416 sheet holding eight props gives each one about 100px. The smallest
prop in the game needs 180. Cropping them is not an option — this is the
"do not extrapolate high resolution from cropped contact sheets" rule in the
backlog, and it is the reason nothing was installed.

---

## 1. There is a second defect, independent of style

The shipped props are also **too small for the room view**, and have been since
before the redesign. A room is composed in an 800x500 space; the focus view
draws that space at `FS = 1500/800 = 1.875`, and an iPad at DPR 2 doubles it
again. So a prop placed at width 320 is painted across roughly 1200 device
pixels from a 229-pixel PNG.

Measured across all 39 props: **every one is 2.2x to 2.9x under size**. That is
why the furniture looks soft next to the crisp characters even before the
colours are considered. Redrawing them at the sizes below fixes both problems
at once.

Needed source width = `drawn width x 1.875 x 2`, rounded to 10.

---

## 2. Style

> **Corrected 2026-11-07, after batch 01.** The first version of this section
> said "the rooms are navy with neon signage" and asked for a cool ambient on
> every prop. That is true of `code` and `ops` and of no other room. Batch 01
> followed it exactly and produced dark slabs that punch holes in the white
> `test` lab and read as empty boxes in the amber `library`. The audit, with
> pictures, is in [audit/propsBatch01Report.md](audit/propsBatch01Report.md).

Open two files before drawing anything:

- **[audit/props-current.jpg](audit/props-current.jpg)** — all 39 props that
  ship today. This is the technique to match and the content to keep.
- **[audit/room-light-reference.jpg](audit/room-light-reference.jpg)** — the
  eight backgrounds, side by side.

### 2.1 Painted, not vector

The rooms are painted illustrations: soft shading, a light direction, bounced
light, reflective floors. A prop must be painted the same way. Flat fills with
a hard outline read as a UI widget pasted onto a photograph — that is the whole
of what went wrong in batch 01, and no amount of correct sizing rescues it.

The existing props have the right technique. What they lack is resolution and,
in a few rooms, the right colour temperature.

### 2.2 Each room has its own light

There is no single palette. Match the room the prop stands in:

| Room | The background actually is | Props should be |
|---|---|---|
| `plan` | cool blue and lavender, mid-bright | light, cool, a cyan accent |
| `library` | **warm amber** wood and lantern light | warm wood, warm highlights |
| `code` | violet and magenta neon, mid-dark | mid value, violet rim |
| `test` | **bright white and cyan lab**, high key | **light** — dark props punch holes |
| `ops` | deep blue with cyan server glow | the one genuinely dark room |
| `deploy` | hot magenta and yellow hazard, saturated | mid, magenta accent |
| `approval` | pale mint and green, bright | light, soft green |
| `lounge` | rose, peach and amber, warm and bright | warm, soft |

A prop used in more than one room (`desk-white2` is in code, ops and test)
takes the lighter reading, so it survives the bright room.

### 2.3 Keep what the prop contains

A prop file is usually a **group**, not a single object. `desk-blue2` is a desk
*and* a chair, a monitor, a small robot, a stack of books and a plant.
`shelf-big` is a bookcase *and* its books, a lamp, a framed picture, storage
boxes and a trailing vine. Replacing them with a bare desk and a bare shelf
empties the room — the chairs and the robots vanish and nothing puts them back.

`audit/props-current.jpg` is the inventory. The redraw must hold the same
objects in the same arrangement.

### 2.4 The rest

- **Outline** in the same weight as the characters, so a prop and an agent
  standing beside it belong to one drawing — but an outline on painted
  shading, not an outline instead of it.
- **Chunky and simplified.** At map scale a room is 440px wide and everything
  in it is a sixth of its focus size.
- Screens, strips and panels may glow; nothing else emits light.
- No characters, no text, no logos drawn into a prop. (The little robots in
  `desk-*` are existing scenery and stay — they are not the agent cast.)

## 3. Canvas rules

These are the same as `ASSET_SPEC.md` and are not negotiable:

- **One object per PNG**, true alpha, cut out — not on a white or chroma plate,
  and not a sheet. A sheet means someone has to guess the cut lines.
- Trim to the object: no transparent margin, because the code anchors by the
  file's own edges.
- The object stands on the **bottom edge** of its canvas, horizontally centred.
- File names are fixed below and must match exactly. `web/assets_test.go`
  rejects anything outside `^[A-Za-z0-9-]{1,40}\.(png|jpg)$`, and a renamed file
  is embedded in the binary but answers 404.

---

## 4. The 39 props, by room

"Drawn at" is the width in the 800x500 room space, from `roomDefs` in
`web/app.js`. Do not change it — the furniture is positioned against it.

### `code`

| Prop file | Drawn at | Needed source | Shipped now | Verdict |
|---|---:|---:|---:|---|
| `plant-big.png` | 74 | **280** wide | 109x157 | **2.6x too small** |
| `desk-blue2.png` | 180 | **680** wide | 244x217 | **2.8x too small** |
| `desk-pink2.png` | 180 | **680** wide | 235x214 | **2.9x too small** |
| `desk-white2.png` | 180 | **680** wide | 237x212 | **2.9x too small** |
| `cactus.png` | 48 | **180** wide | 78x128 | **2.3x too small** |

### `test`

| Prop file | Drawn at | Needed source | Shipped now | Verdict |
|---|---:|---:|---:|---|
| `filing-blue.png` | 70 | **260** wide | 117x197 | **2.2x too small** |
| `desk-white2.png` | 180 | **680** wide | 237x212 | **2.9x too small** |
| `desk-blue2.png` | 180 | **680** wide | 244x217 | **2.8x too small** |
| `printer2.png` | 116 | **440** wide | 171x153 | **2.6x too small** |
| `hydrangea.png` | 64 | **240** wide | 101x116 | **2.4x too small** |

### `library`

| Prop file | Drawn at | Needed source | Shipped now | Verdict |
|---|---:|---:|---:|---|
| `shelf-big.png` | 210 | **790** wide | 281x234 | **2.8x too small** |
| `shelf-tall.png` | 96 | **360** wide | 119x230 | **3.0x too small** |
| `globe.png` | 56 | **210** wide | 88x112 | **2.4x too small** |
| `table-meet.png` | 232 | **870** wide | 245x205 | **3.6x too small** |
| `plant-stand.png` | 66 | **250** wide | 91x136 | **2.7x too small** |
| `books2.png` | 70 | **260** wide | 103x93 | **2.5x too small** |

### `plan`

| Prop file | Drawn at | Needed source | Shipped now | Verdict |
|---|---:|---:|---:|---|
| `desk-boss2.png` | 240 | **900** wide | 326x227 | **2.8x too small** |
| `table-meet.png` | 226 | **850** wide | 245x205 | **3.5x too small** |
| `plant-big.png` | 70 | **260** wide | 109x157 | **2.4x too small** |
| `calendar.png` | 56 | **210** wide | 107x83 | **2.0x too small** |

### `approval`

| Prop file | Drawn at | Needed source | Shipped now | Verdict |
|---|---:|---:|---:|---|
| `reception2.png` | 260 | **980** wide | 369x184 | **2.7x too small** |
| `sofa-yellow2.png` | 270 | **1010** wide | 308x148 | **3.3x too small** |
| `water.png` | 58 | **220** wide | 97x197 | **2.3x too small** |
| `flowers-pot.png` | 52 | **200** wide | 86x111 | **2.3x too small** |
| `pillow-bunny.png` | 52 | **200** wide | 74x70 | **2.7x too small** |

### `ops`

| Prop file | Drawn at | Needed source | Shipped now | Verdict |
|---|---:|---:|---:|---|
| `servers2.png` | 196 | **740** wide | 252x224 | **2.9x too small** |
| `filing-blue.png` | 66 | **250** wide | 117x197 | **2.1x too small** |
| `desk-white2.png` | 180 | **680** wide | 237x212 | **2.9x too small** |
| `printer2.png` | 104 | **390** wide | 171x153 | **2.3x too small** |
| `plant-big.png` | 70 | **260** wide | 109x157 | **2.4x too small** |

### `deploy`

| Prop file | Drawn at | Needed source | Shipped now | Verdict |
|---|---:|---:|---:|---|
| `desk-pink2.png` | 176 | **660** wide | 235x214 | **2.8x too small** |
| `desk-pink2.png` | 176 | **660** wide | 235x214 | **2.8x too small** |
| `rug-pink2.png` | 320 | **1200** wide | 229x125 | **5.2x too small** |
| `rocket.png` | 96 | **360** wide | 190x141 | **1.9x too small** |
| `bean-pink.png` | 84 | **320** wide | 140x121 | **2.3x too small** |
| `bean-yellow.png` | 84 | **320** wide | 139x123 | **2.3x too small** |
| `box-pallet.png` | 74 | **280** wide | 187x135 | **1.5x too small** |
| `handtruck.png` | 54 | **200** wide | 116x130 | **1.7x too small** |

### `lounge`

| Prop file | Drawn at | Needed source | Shipped now | Verdict |
|---|---:|---:|---:|---|
| `rug-cloud.png` | 250 | **940** wide | 190x143 | **4.9x too small** |
| `sofa-pink2.png` | 276 | **1040** wide | 321x148 | **3.2x too small** |
| `armchair2.png` | 112 | **420** wide | 148x168 | **2.8x too small** |
| `hammock2.png` | 236 | **880** wide | 253x160 | **3.5x too small** |
| `bean-purple.png` | 76 | **280** wide | 148x126 | **1.9x too small** |
| `water.png` | 52 | **200** wide | 97x197 | **2.1x too small** |
| `plant-stand.png` | 58 | **220** wide | 91x136 | **2.4x too small** |

### `entrance`

| Prop file | Drawn at | Needed source | Shipped now | Verdict |
|---|---:|---:|---:|---|
| `signboard.png` | 120 | **450** wide | 102x114 | **4.4x too small** |
| `sakura2.png` | 240 | **900** wide | 169x151 | **5.3x too small** |
| `bench2.png` | 190 | **710** wide | 154x101 | **4.6x too small** |
| `flowerbox3.png` | 150 | **560** wide | 136x83 | **4.1x too small** |
| `lamppost.png` | 46 | **170** wide | 49x150 | **3.5x too small** |
| `bush2b.png` | 160 | **600** wide | 155x84 | **3.9x too small** |

---

## 5. `entrance` — decide before drawing

`entrance` is a ninth scene the redesign mockups never showed
(`REDESIGN_SPEC.md` §1). Its six props are listed above for completeness. If
the redesign drops the garden, they are not worth drawing; if it keeps it, the
scene needs a style decision of its own, because a sakura garden at night is
not the same problem as a navy office.

## 6. How a delivery is checked

```sh
go run ./tools/assetaudit -in <folder> -out audit.json -sheet contact.png
go test ./...
```

Then each room is composed and looked at, because size and alpha are the only
things the tools can judge. A prop passes when it is the right size, cut out,
and does not look lit by a different room than the one it stands in.

Batch 01 passed every automated check — all ten widths exact, real alpha, tight
crop, correct names — and still had to be rejected on sight. Composing it into
the real rooms is what settled it, and that is one command:

```sh
# old and new side by side, over the real background, at the real positions
go run ./tools/roomcompare -new <folder> -rooms code,test,library -out cmp.png
```

Raw generator output goes through `tools/propprep` first — it trims to the
alpha, scales to the width `roomDefs` draws the prop at, and quantises to a
256-colour palette, which is worth about 4x on the wire for painted art and is
invisible at the size the prop is actually shown:

```sh
go run ./tools/propprep -in <raw folder> -out /tmp/ready \
  -map 01=desk-blue2,02=desk-pink2,...
```

Props do not fail on their own; they fail in the room.

The five character sheets in `aiagent_art_rebuild_starter_20261107.zip` stay
uninstalled until all 36 arrive — the pack's own README says so, and half a
cast in each style is the one outcome to avoid.

Licensing: record the tool, the account and the terms before any of this is
committed. See `ASSETS.md`.
