# Village brief — the map outside the house

> **Status, 2026-11-07.** The **ground is done**: `bg-village.jpg` is a
> painted neon plaza under a night city skyline, and the SVG island, the
> pastel sky, the cartoon clouds and the duplicated pastel trees, bushes and
> fences are no longer drawn over it. See
> [audit/village-painted.jpg](audit/village-painted.jpg).
>
> What is left on the map is **the five houses**, which are still the pastel
> cottages. Everything else either got redrawn for the entrance scene or is
> now part of the painting.
>
> Three other base images arrived and were not used, kept in `docs/incoming`:
> `village-base-1-terrace` is a terrace, not a map; `-3-town` and `-4-island`
> have the buildings painted into them, so the houses could not be separate
> images that the code positions, labels and makes clickable. Using one of
> those means moving the village to a flat painting with hit boxes over the
> painted buildings — a different design, and a bigger change than swapping a
> background. It is a real option and `-4-island` matches the mockup most
> closely; it just is not this change.


The eight rooms and the house around them are in the new design. The **village
map**, which is the first screen you see every time, is not: a pastel sky over
a green hill with cottage houses on it.

Unlike the house, this one cannot be fixed in CSS. The sky, the floating
island, the paths and the plaza *are* CSS and SVG and could be repainted in an
afternoon — but the twenty-one things standing on them are PNGs from the
pastel set, and a neon night sky behind cream cottages looks worse than the
pastel sky does. So the background stays as it is until the art lands, and
then both change together.

Read [PROP_BRIEF.md](PROP_BRIEF.md) §2 first: the style rules, the
painted-not-vector rule and the keep-what-the-file-contains rule are the same
here, and so is `tools/propprep` — raw output at any size above the numbers
below, and it trims, scales and quantises.

---

## What the village is

A floating island in the sky, reached by a gate, with a plaza and paths. Each
house is one place agents work from:

| House | What it is |
|---|---|
| `house-kcmd` | the agcmd house — tmux, takes work from the web |
| `house-bg` | the Background house — the Claude daemon, messages go through the relay |
| `house-term` | the Terminal / VS Code house — read only |
| `house2-pink` | Deploy — lit while something is building |
| `house2-blue` | Infra |
| `house2-green`, `house2-orange`, `house2-purple` | spare colours for further houses |

## Size

The village board is 1660x940 and is **scaled down** to fit the stage — it is
never drawn larger than its nominal size, unlike a room in the focus view. So
the source needs the drawn width **x2**, for a retina screen, and no more.

Several houses are already close: the `house2-*` set is 410–538 wide against
the 580–780 it wants, so those are a redraw for style rather than for size.
The three `house-*` cottages are around 200px and genuinely too small.

### Houses

| File | Drawn at | Needed source | Shipped now |
|---|---:|---:|---:|
| `house2-purple.png` | 390 | **780** | 538x366 |
| `house-bg.png` | 380 | **760** | 240x151 |
| `house-kcmd.png` | 330 | **660** | 203x143 |
| `house-term.png` | 330 | **660** | 206x146 |
| `house2-green.png` | 320 | **640** | 458x342 |
| `house2-orange.png` | 320 | **640** | 456x366 |
| `house2-pink.png` | 300 | **600** | 430x364 |
| `house2-blue.png` | 290 | **580** | 410x364 |

### Scenery

| File | Drawn at | Needed source | Shipped now |
|---|---:|---:|---:|
| `gate.png` | 380 | **760** | 336x87 |
| `sakura2.png` | 230 | **460** | 900x708 | **done**
| `sakura.png` | 170 | **340** | 107x102 |
| `fountain.png` | 160 | **320** | 117x86 |
| `fence.png` | 130 | **260** | 101x42 |
| `tree1.png` | 120 | **240** | 87x93 |
| `tree2.png` | 120 | **240** | 89x100 |
| `flowerbox3.png` | 120 | **240** | 563x425 | **done**
| `bench2.png` | 120 | **240** | 713x514 | **done**
| `board-cork.png` | 110 | **220** | 189x165 |
| `bush2.png` | 80 | **160** | 59x48 |
| `bush3.png` | 80 | **160** | 60x48 |
| `bush2b.png` | 80 | **160** | 600x437 | **done**
| `bush1.png` | 60 | **120** | 53x48 |
| `lamppost2.png` | 44 | **88** | 51x154 |

Four are already done — they were drawn for the entrance scene and the village
uses the same files.

---

## Style

Same world as the rooms: night, neon, sakura. `docs/audit/interior-neon.jpg`
shows the house interior as it now looks, and the village has to read as the
outside of that building, seen from the air.

- **Night, not daylight.** The rooms glow because they are lit windows in the
  dark. A village under a blue noon sky cannot contain them.
- **Each house is a lit building**: dark structure, windows that glow, one
  neon sign or rim in its own colour — pink for Deploy, blue for Infra, and so
  on from the table above.
- **Painted, with a light direction**, like the props. Not flat vector.
- Keep what each file contains: `fountain` has its bunny statue, `gate` has
  its sign, `board-cork` has its pinned notes.

## What stays CSS

Do not commission art for these — they are drawn by the code and will be
repainted with the same change that installs the art:

- the sky, the stars, the sun and moon, the drifting clouds
- the floating island: its grass, its two stone tiers, the brick pattern, the
  plaza and the paths
- the falling petals and the fireflies
- the weather tint

`cloud1` and `cloud2` are PNGs but are 61px wide and barely visible; they can
stay or be dropped, as the drawing suggests.

## How it is checked

```sh
go run ./tools/propprep -in <raw folder> -out /tmp/ready -map 01=house-kcmd,...
go test ./...
```

Then the village is opened and looked at, the same way the rooms were. There
is no `roomcompare` for this scene yet; if a second batch is needed it is
worth adding one, because the first prop batch proved that props do not fail
on their own — they fail in the scene.
