# Character brief — all 36 sheets, new design

The owner chose the redesign (2026-11-07), so the whole cast is re-rendered in
it. Half a cast in each style is the one outcome to avoid, which is why this
is 36 files and not five.

Hand this file and `audit/style-reference.png` to whoever generates the art.

---

## 1. The six roles

`audit/style-reference.png` has six panels.

**Panels 1–3 are the style.** They are already in the new design and define it:
chunky chibi proportions, heavy dark outline, neon rim light, cyberpunk
streetwear, a glowing device in hand, bold saturated hair.

| Panel | Role | Who they are |
|---|---|---|
| 1 | `dev` | Blue spiked hair, headphones, dark jacket, glowing laptop |
| 2 | `designer` | Dark hair with pink tips, pink headphones, holographic jacket, artist palette, colour swatches, stylus bag |
| 3 | `tester` | Brown ponytail, goggles pushed up on the head, white jacket, clipboard with a tick |

**Panels 4–6 are the three still in the old style.** Keep each concept exactly
— the role must stay readable — and redraw them to match panels 1–3:

| Panel | Role | Keep | Restyle |
|---|---|---|---|
| 4 | `research` | Silver-white hair, round glasses, lab coat, headphones, satchel of vials | Same person, new rendering: heavier outline, neon rim light, bolder hair |
| 5 | `infra` | Yellow hard hat, hi-vis jacket, tool belt, wrench, tablet | As above |
| 6 | `deploy` | White robot with a glowing visor face, orange cap, courier backpack | As above |

Six distinct silhouettes, all the same height and build so they share one
floor line. Tell them apart by colour, headgear and prop — never by size.

---

## 2. The 36 files

Six roles x six animations. **Deliver with these exact names.**

```
ch-dev-work.png        ch-dev-think.png        ch-dev-rest.png
ch-dev-cheer.png       ch-dev-walkL.png        ch-dev-walkR.png

ch-research-work.png   ch-research-think.png   ch-research-rest.png
ch-research-cheer.png  ch-research-walkL.png   ch-research-walkR.png

ch-tester-work.png     ch-tester-think.png     ch-tester-rest.png
ch-tester-cheer.png    ch-tester-walkL.png     ch-tester-walkR.png

ch-infra-work.png      ch-infra-think.png      ch-infra-rest.png
ch-infra-cheer.png     ch-infra-walkL.png      ch-infra-walkR.png

ch-deploy-work.png     ch-deploy-think.png     ch-deploy-rest.png
ch-deploy-cheer.png    ch-deploy-walkL.png     ch-deploy-walkR.png

ch-designer-work.png   ch-designer-think.png   ch-designer-rest.png
ch-designer-cheer.png  ch-designer-walkL.png   ch-designer-walkR.png
```

### What each animation shows

| Animation | Frames | Reads as |
|---|---:|---|
| `work` | 4 | Heads-down and busy. Typing, adjusting, writing — hands moving on the prop |
| `think` | 4 | Paused and considering. A question mark or lightbulb beat is welcome |
| `rest` | 4 | Idle and relaxed. Breathing, a blink, weight shifting. **Standing still, not asleep** |
| `cheer` | 4 | Just finished something. Arm up, a jump, sparkles |
| `walkL` | 6 | Walking, **side profile, facing LEFT** |
| `walkR` | 6 | Walking, **side profile, facing RIGHT** |

---

## 3. Canvas and grid

- Background **flat chroma green `#00FF00`**, nothing else on it.
- Frames in **one horizontal row**, left to right, equal width, no gaps, no
  padding, no captions in the image.
- Any resolution is fine as long as the row divides evenly: a 6-frame sheet at
  3072x512 and a 4-frame sheet at 2508x627 both worked. Square-ish frames are
  easiest to fit.
- The character **stands on the bottom edge** and is horizontally centred in
  its own cell, the same way in every frame. Nothing may touch a cell's left
  or right edge.
- The conversion to the shipped 768x128 / 512x128 is done here by
  `tools/spritesheet`; it is not your job to hit those sizes.

---

## 4. The five things that have gone wrong before

Each of these passed every size check and still had to be rejected.

1. **A walk that does not walk.** Six frames of the same front-facing stand.
   It looks like an animation in a thumbnail and slides across the floor when
   played. **Draw walks in side profile with the legs clearly alternating** —
   one leg forward and one back, swapping across the cycle.
2. **A repeated frame.** Two frames identical in both shape and colour, so the
   loop stutters. Every frame must differ from every other frame in its sheet.
3. **One file reused across animations.** `ch-infra-cheer`, `-rest` and
   `-work` arrived byte-identical, so infra looked the same whether it was
   celebrating or asleep. **All 36 files must be different images.**
4. **The character changing between animations.** The last batch gave a
   blue-haired developer for three animations while the other three were
   pink-haired. **One role is one person across all six of its sheets** —
   same hair, same outfit, same prop.
5. **Content touching a cell edge**, which means the grid does not match the
   art and the slice shows a sliver of the next pose.

---

## 5. How it is checked on arrival

```sh
go run ./tools/assetaudit  -in <folder> -out audit.json -sheet contact.png
go run ./tools/spritesheet -in <sheet>.png -frames 6 -out /tmp/check.png
```

The audit measures frame count, per-frame content, repeated frames (shape and
colour), and edge bleed. Then the converted sheet is opened and the legs are
watched, because three different automated walk checks have each given a wrong
answer on a real sheet — the numbers narrow it down, the eye decides.

Licensing: record the tool, the account and the terms in force before any of
this is committed. See `ASSETS.md`.
