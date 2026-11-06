# Asset specification — for a clean-licence replacement set

Every image in `web/assets/` is to be **regenerated from scratch** so the set can
ship under Apache-2.0 alongside the code, replacing the current images whose
provenance was never recorded (see [ASSETS.md](ASSETS.md)).

**You do not have to match the old pictures.** Match the *slots*: the file name,
the canvas size, and the role the image plays. Anything that fills those three
correctly drops in with **no code change** — the serving path is a plain
allowlist over this directory.

## Hard rules

| Rule | Why |
|---|---|
| **File names exactly as listed.** `^[A-Za-z0-9-]{1,40}\.(png\|jpg)$` | `assetName` in `http.go` refuses anything else; a renamed file 404s silently |
| **PNG with real alpha** for everything except `bg-*`, which is **JPG** | Props are composited over room backgrounds; a white box instead of alpha is the most common failure |
| **Exact canvas sizes** in the tables below | Layout positions are hard-coded in `web/app.js` |
| **One object per file**, centred, trimmed | Padding reads directly as a floating or misaligned prop |
| **No logos or brand marks** — not Anthropic's, not OpenAI's, not any real company's | The UI deliberately uses neutral `✻` and `>_` marks |
| **No real people's likenesses, no recognisable characters** | The whole point of regenerating is a clean licence |
| Keep the total near the current **4.6 MB** | The whole set is embedded in the binary |

> **Record provenance as you go.** For each batch write down the tool, the
> account, the date, and the terms in force — that table in `ASSETS.md` is the
> thing that makes the licence real. A set of images with no record is exactly
> the problem being fixed.

## Art direction

A warm, cosy, **isometric** office village. Soft pastels — dusty pink, butter
yellow, sage, lilac, pale blue — with a few saturated accents on the things that
matter (a rocket, a status light, the active screen). Rounded shapes, soft
shadows, no hard black outlines, no gradients harsher than a gentle ambient
occlusion. It should read as a friendly toy town, not a corporate diagram.

Lighting is **top-left**, consistently, across every prop. The camera is a fixed
isometric three-quarter view: props are drawn as if seen from above and slightly
to the front-left. Each prop **stands on the bottom edge of its own canvas**.

## The eight rooms

They appear in workflow order and each has three files that must agree in mood:
`bg-<room>.jpg` (the room), `sky-<room>.png` (what the window shows), and
`plq-<room>.png` (the plaque above the door).

| Room key | Thai label | What happens there | Mood |
|---|---|---|---|
| `plan` | ห้องวางแผน | Planning, task boards | Morning light, clean desks, cork board |
| `library` | ห้องวิจัย | Research and reading | Warm wood, bookshelves, reading lamps |
| `code` | ห้องพัฒนา | Writing code | Focused, monitors, cooler blue cast |
| `test` | ห้องทดสอบ | Running tests | Bright, clinical but friendly, checklists |
| `ops` | ห้องระบบ | Infrastructure, monitoring | Server racks, status lights, cooler |
| `deploy` | ห้อง Deploy | Shipping | Energetic, a launch pad, the rocket |
| `approval` | รอคุณตอบ | Waiting for the human | Calm, a quiet sofa, a soft waiting light |
| `lounge` | ห้องพัก | Resting between jobs | Cosiest — plants, hammock, tea, low light |

`bg-*.jpg` is **1600x1000**, drawn as the back wall and floor of an isometric
room with the **middle-left to middle-right kept clear**: characters walk across
that band and props are layered on top by code. Do not draw characters into the
background.

## Agent sprite sheets — read this twice

Six roles x six animations = **36 sheets**. These are the only animated assets and
the only ones where getting the grid wrong breaks things visibly.

- Every frame is **128 x 128**, laid out in a **single horizontal row**, no padding,
  no gaps, no margin.
- `walkL` and `walkR` have **6 frames** → canvas **768 x 128**.
- `work`, `think`, `rest`, `cheer` have **4 frames** → canvas **512 x 128**.
- The character stands on the **bottom edge** of each frame and is **horizontally
  centred** in its own 128 px cell. It must not drift between frames or the walk
  cycle wobbles.
- `walkL` faces **left**, `walkR` faces **right**. They are separate drawings, not
  a mirrored pair — but mirroring is acceptable if the character is symmetric.
- The loop must be **seamless**: frame 6 leads back into frame 1.

| Role | Who they are |
|---|---|
| `dev` | Writes code. The most-seen character |
| `research` | Reads and investigates |
| `tester` | Runs tests, carries a clipboard |
| `infra` | Watches systems, a toolbelt |
| `deploy` | Ships things, a helmet or courier look |
| `designer` | Visual work |

| Animation | Reads as | Frames |
|---|---|---|
| `work` | Heads-down, typing or doing | 4 |
| `think` | Paused, considering — slower | 4 |
| `rest` | Idle, breathing, waiting — slowest | 4 |
| `cheer` | Finished something, celebrating | 4 |
| `walkL` / `walkR` | Walking between rooms | 6 each |

Keep all six roles the **same height and build** so they line up on one floor.
Distinguish them by colour, tool and silhouette detail, not by size.

## Priority

If the set has to arrive in batches, this is the order that keeps the UI usable:

1. **The 36 `ch-*` sprite sheets** — without them nothing moves
2. **The 8 `bg-*` backgrounds** — the rooms themselves
3. **`plq-*`, `sky-*`, `scr-*`, `fx-*`** — labels, windows, screens, status
4. **`cx-*` standees** — decorative crowd; the UI survives placeholders
5. **Furniture, plants, scenery** — pure decoration

---

# The full file list

Every file below must exist with exactly this name and size.

### Room backgrounds (8)

| File | Size |
|---|---|
| `bg-approval.jpg` | 1600x1000 |
| `bg-code.jpg` | 1600x1000 |
| `bg-deploy.jpg` | 1600x1000 |
| `bg-library.jpg` | 1600x1000 |
| `bg-lounge.jpg` | 1600x1000 |
| `bg-ops.jpg` | 1600x1000 |
| `bg-plan.jpg` | 1600x1000 |
| `bg-test.jpg` | 1600x1000 |

### Room skies (seen through windows) (8)

| File | Size |
|---|---|
| `sky-approval.png` | 800x500 |
| `sky-code.png` | 800x500 |
| `sky-deploy.png` | 800x500 |
| `sky-library.png` | 800x500 |
| `sky-lounge.png` | 800x500 |
| `sky-ops.png` | 800x500 |
| `sky-plan.png` | 800x500 |
| `sky-test.png` | 800x500 |

### Room name plaques (8)

| File | Size |
|---|---|
| `plq-approval.png` | 336x124 |
| `plq-code.png` | 329x122 |
| `plq-deploy.png` | 334x124 |
| `plq-library.png` | 329x123 |
| `plq-lounge.png` | 333x124 |
| `plq-ops.png` | 331x124 |
| `plq-plan.png` | 330x122 |
| `plq-test.png` | 332x123 |

### Agent sprite sheets (36)

| File | Size |
|---|---|
| `ch-deploy-cheer.png` | 512x128 |
| `ch-deploy-rest.png` | 512x128 |
| `ch-deploy-think.png` | 512x128 |
| `ch-deploy-walkL.png` | 768x128 |
| `ch-deploy-walkR.png` | 768x128 |
| `ch-deploy-work.png` | 512x128 |
| `ch-designer-cheer.png` | 512x128 |
| `ch-designer-rest.png` | 512x128 |
| `ch-designer-think.png` | 512x128 |
| `ch-designer-walkL.png` | 768x128 |
| `ch-designer-walkR.png` | 768x128 |
| `ch-designer-work.png` | 512x128 |
| `ch-dev-cheer.png` | 512x128 |
| `ch-dev-rest.png` | 512x128 |
| `ch-dev-think.png` | 512x128 |
| `ch-dev-walkL.png` | 768x128 |
| `ch-dev-walkR.png` | 768x128 |
| `ch-dev-work.png` | 512x128 |
| `ch-infra-cheer.png` | 512x128 |
| `ch-infra-rest.png` | 512x128 |
| `ch-infra-think.png` | 512x128 |
| `ch-infra-walkL.png` | 768x128 |
| `ch-infra-walkR.png` | 768x128 |
| `ch-infra-work.png` | 512x128 |
| `ch-research-cheer.png` | 512x128 |
| `ch-research-rest.png` | 512x128 |
| `ch-research-think.png` | 512x128 |
| `ch-research-walkL.png` | 768x128 |
| `ch-research-walkR.png` | 768x128 |
| `ch-research-work.png` | 512x128 |
| `ch-tester-cheer.png` | 512x128 |
| `ch-tester-rest.png` | 512x128 |
| `ch-tester-think.png` | 512x128 |
| `ch-tester-walkL.png` | 768x128 |
| `ch-tester-walkR.png` | 768x128 |
| `ch-tester-work.png` | 512x128 |

### Standees — robots, kids, animals (42)

| File | Size |
|---|---|
| `cx-bear.png` | 64x85 |
| `cx-bot1.png` | 93x104 |
| `cx-bot10.png` | 84x104 |
| `cx-bot11.png` | 81x104 |
| `cx-bot12.png` | 69x83 |
| `cx-bot13.png` | 81x83 |
| `cx-bot14.png` | 76x81 |
| `cx-bot15.png` | 65x80 |
| `cx-bot16.png` | 68x82 |
| `cx-bot17.png` | 83x74 |
| `cx-bot18.png` | 81x76 |
| `cx-bot19.png` | 70x80 |
| `cx-bot2.png` | 88x104 |
| `cx-bot20.png` | 71x81 |
| `cx-bot21.png` | 74x87 |
| `cx-bot22.png` | 71x85 |
| `cx-bot23.png` | 84x84 |
| `cx-bot24.png` | 71x80 |
| `cx-bot3.png` | 80x104 |
| `cx-bot4.png` | 80x104 |
| `cx-bot5.png` | 79x104 |
| `cx-bot6.png` | 82x104 |
| `cx-bot7.png` | 77x105 |
| `cx-bot8.png` | 81x105 |
| `cx-bot9.png` | 83x105 |
| `cx-bunny.png` | 65x101 |
| `cx-cat.png` | 68x92 |
| `cx-deer.png` | 60x88 |
| `cx-elephant.png` | 89x85 |
| `cx-fox.png` | 67x91 |
| `cx-husky.png` | 69x92 |
| `cx-kid0.png` | 69x101 |
| `cx-kid1.png` | 72x102 |
| `cx-kid2.png` | 77x104 |
| `cx-kid3.png` | 68x101 |
| `cx-kid4.png` | 72x101 |
| `cx-kid5.png` | 71x102 |
| `cx-kid6.png` | 68x103 |
| `cx-kid7.png` | 71x94 |
| `cx-kid8.png` | 70x97 |
| `cx-panda.png` | 71x90 |
| `cx-penguin.png` | 73x88 |

### Houses and exteriors (8)

| File | Size |
|---|---|
| `house-bg.png` | 240x151 |
| `house-kcmd.png` | 203x143 |
| `house-term.png` | 206x146 |
| `house2-blue.png` | 410x364 |
| `house2-green.png` | 458x342 |
| `house2-orange.png` | 456x366 |
| `house2-pink.png` | 430x364 |
| `house2-purple.png` | 538x366 |

### Wall screens / TVs (7)

| File | Size |
|---|---|
| `scr-blue.png` | 262x149 |
| `scr-bunny.png` | 229x177 |
| `scr-lamp.png` | 276x157 |
| `scr-robot.png` | 296x164 |
| `scr-tablet.png` | 150x115 |
| `scr-white.png` | 241x151 |
| `tv-robot.png` | 293x190 |

### Status effects (5)

| File | Size |
|---|---|
| `fx-bang.png` | 44x43 |
| `fx-bulb.png` | 35x45 |
| `fx-check.png` | 41x40 |
| `fx-q.png` | 43x43 |
| `fx-zz.png` | 43x39 |

### Signposts and arrows (16)

| File | Size |
|---|---|
| `arr-big.png` | 87x61 |
| `arr-branch.png` | 149x91 |
| `arr-dash.png` | 127x34 |
| `arr-pill.png` | 86x52 |
| `arr-pill2.png` | 86x55 |
| `arr-pill3.png` | 94x55 |
| `arr-turn.png` | 97x58 |
| `post-link.png` | 96x113 |
| `post-right.png` | 95x112 |
| `post-swap.png` | 95x113 |
| `post-tree.png` | 95x112 |
| `tile-check.png` | 129x59 |
| `tile-feet.png` | 132x67 |
| `tile-fwd.png` | 143x70 |
| `tile-pin.png` | 105x88 |
| `tile-share.png` | 142x70 |

### Furniture, plants and scenery (92)

| File | Size |
|---|---|
| `arch-hall.png` | 199x177 |
| `arch-hall2.png` | 145x165 |
| `armchair-lilac.png` | 104x107 |
| `armchair-yellow.png` | 96x105 |
| `armchair2.png` | 148x168 |
| `bean-pink.png` | 140x121 |
| `bean-purple.png` | 148x126 |
| `bean-yellow.png` | 139x123 |
| `bench.png` | 95x64 |
| `bench2.png` | 154x101 |
| `board-cork.png` | 189x165 |
| `board.png` | 85x78 |
| `books2.png` | 103x93 |
| `box-pallet.png` | 187x135 |
| `bush-pot2.png` | 67x86 |
| `bush1.png` | 53x48 |
| `bush2.png` | 59x48 |
| `bush2b.png` | 155x84 |
| `bush3.png` | 60x48 |
| `cactus.png` | 78x128 |
| `calendar.png` | 107x83 |
| `cloud1.png` | 61x37 |
| `cloud2.png` | 63x37 |
| `crate-lilac.png` | 92x110 |
| `daisy-pot.png` | 65x84 |
| `desk-blue2.png` | 244x217 |
| `desk-boss2.png` | 326x227 |
| `desk-pink2.png` | 235x214 |
| `desk-white2.png` | 237x212 |
| `door-blue2.png` | 95x160 |
| `door-glass.png` | 171x165 |
| `door-pink2.png` | 98x161 |
| `easel-check.png` | 144x181 |
| `fence.png` | 101x42 |
| `filing-blue.png` | 117x197 |
| `flowerbox3.png` | 136x83 |
| `flowers-pot.png` | 86x111 |
| `fountain.png` | 117x86 |
| `gate.png` | 336x87 |
| `globe.png` | 88x112 |
| `hammock2.png` | 253x160 |
| `handtruck.png` | 116x130 |
| `hedge.png` | 87x37 |
| `hydrangea.png` | 101x116 |
| `hydrangea2.png` | 74x98 |
| `lamp1.png` | 31x85 |
| `lamp2.png` | 30x69 |
| `lamppost.png` | 49x150 |
| `lamppost2.png` | 51x154 |
| `lantern.png` | 57x103 |
| `lattice.png` | 127x168 |
| `pendant.png` | 69x78 |
| `pendant2.png` | 66x105 |
| `pillar-thin.png` | 43x164 |
| `pillar.png` | 60x171 |
| `pillow-bunny.png` | 74x70 |
| `plant-big.png` | 109x157 |
| `plant-sprout.png` | 71x102 |
| `plant-stand.png` | 91x136 |
| `plant-tall2.png` | 102x130 |
| `planter1.png` | 64x51 |
| `planter2.png` | 57x48 |
| `printer2.png` | 171x153 |
| `reception2.png` | 369x184 |
| `rocket.png` | 190x141 |
| `rug-cloud.png` | 190x143 |
| `rug-cream.png` | 179x95 |
| `rug-lilac.png` | 177x79 |
| `rug-pink2.png` | 229x125 |
| `rug-rose.png` | 168x85 |
| `sakura.png` | 107x102 |
| `sakura2.png` | 169x151 |
| `sconce.png` | 86x87 |
| `servers2.png` | 252x224 |
| `shelf-big.png` | 281x234 |
| `shelf-tall.png` | 119x230 |
| `signboard.png` | 102x114 |
| `sofa-bunny.png` | 218x113 |
| `sofa-pink2.png` | 321x148 |
| `sofa-yellow2.png` | 308x148 |
| `stones.png` | 129x73 |
| `succulent.png` | 63x75 |
| `table-meet.png` | 245x205 |
| `tree1.png` | 87x93 |
| `tree2.png` | 89x100 |
| `trellis.png` | 138x168 |
| `vine-stand.png` | 74x95 |
| `wall-corner.png` | 192x176 |
| `wall-plain.png` | 187x171 |
| `wall-win.png` | 281x171 |
| `water.png` | 97x197 |
| `window-big.png` | 171x164 |

---

## Checking a batch before you commit it

**`go test ./web/` checks everything on this page** — names against the serving
allowlist, every sprite-sheet grid, each room's background and sky size, and
that every prop carries real transparency. Run it first; it names the offending
file and says what is wrong with it.

The shell equivalents below are for checking a batch *before* it is copied into
the tree:

```sh
cd web/assets
# names the server will actually serve
ls | grep -vE '^[A-Za-z0-9-]{1,40}\.(png|jpg)$' && echo "BAD NAMES ABOVE"
# sprite sheets must be 512x128 or 768x128
for f in ch-*.png; do
  sips -g pixelWidth -g pixelHeight "$f" | awk -v f="$f" '/pixelWidth/{w=$2}/pixelHeight/{h=$2}END{if(!((w==512||w==768)&&h==128))print "BAD GRID:",f,w"x"h}'
done
# every PNG must carry an alpha channel
for f in *.png; do
  sips -g hasAlpha "$f" | grep -q "hasAlpha: yes" || echo "NO ALPHA: $f"
done
```

Then `go test ./...` — `TestEveryEmbeddedAssetIsServable` checks every file in
this directory against the serving allowlist — and open the village to look at
it. A file that exists but was never looked at is not done.
