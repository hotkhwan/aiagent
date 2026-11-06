# Redesign — what to draw, what to animate, what needs code

Owner mockups, 2026-11-06: a neon-and-sakura "AI Workflow RPG" replacing the
soft pastel village. This document says what each piece actually is, so that
work is not commissioned as art when it is code, or drawn at the wrong size.

Read [ASSET_SPEC.md](ASSET_SPEC.md) first for the file-name, canvas-size and
alpha rules — they still apply to every raster file below.

---

## 1. The good news: the rooms already match

The mockups name eight rooms. The code already has exactly those eight, with
these keys — **use the keys, not the labels**, when naming files:

| Key | Mockup | Thai label in code | Current icon |
|---|---|---|---|
| `plan` | Plan | ห้องวางแผน | — |
| `library` | Library / Research | ห้องวิจัย | — |
| `code` | Code | ห้องพัฒนา | `</>` |
| `test` | Test | ห้องทดสอบ | — |
| `ops` | Ops | ห้องระบบ | layers |
| `deploy` | Deploy | ห้อง Deploy | rocket |
| `approval` | Approval | รอคุณตอบ | — |
| `lounge` | Lounge | ห้องพัก | — |

Nothing has to be renamed, re-keyed or re-routed. There is also a ninth scene
in the code the mockups do not show — `entrance` (ทางเข้า, a garden). Decide
whether the redesign keeps it; if not, say so, because its props are otherwise
orphaned.

## 2. Room icons — these are SVG paths, not images

`iconSvg()` in `web/app.js` draws icons as inline SVG. Do **not** commission
PNGs for these: a drawn icon cannot inherit the room's neon colour, and the
neon glow in the mockups is a CSS effect on a stroked path.

Give each icon as a single `d` path string meeting all of:

- **viewBox `0 0 24 24`**, the whole glyph inside a 20x20 safe area (2px margin)
- **stroke only** — `fill="none"`, `stroke="currentColor"`, `stroke-width="2.2"`,
  round caps and joins. The colour comes from the room, so the path must carry
  none
- **one continuous path** where possible; subpaths with `M` are fine, separate
  `<path>` elements are not
- legible at **15 real px**. The stepper asks for 30, but the strip sits on the
  2060-wide interior board that `applyZoom()` fits to the stage, so every
  nominal size is multiplied by about 0.53 on the way to the screen. An earlier
  draft of this line said "20px, because that is the size in the workflow
  stepper" and did not account for the board fit: at 20 nominal the glyphs land
  near 11 real px, where a clipboard's ticks and a gear's teeth are simply
  gone. Judge a candidate from a screenshot at 15 real px, never from the
  artwork viewed large

All eight now exist in `iconPaths` in `web/app.js` (2026-11-07), from the
owner's `aiagent_art_rebuild_starter` pack, checked at 15 and 20 real px in
headless Chrome:

| Key | Glyph | Status |
|---|---|---|
| `plan` | Clipboard with two task lines and a tick | done |
| `library` | Open book, spine centred | done |
| `code` | `< >` chevrons | done |
| `test` | Conical flask with a level line | done |
| `ops` | Gear, six teeth. Drawn 1–23, so `iconFit` scales it back into the 20x20 safe area | done |
| `deploy` | Rocket, tilted, with flame. The map houses keep the older upright `rocket` | done |
| `approval` | Shield with a tick | done |
| `lounge` | Coffee cup with a handle, no steam (too fine at this size) | done |

`web/assets_test.go::TestEveryRoomHasAGlyph` fails if a room loses its entry —
`iconSvg()` falls back to a bare dash, which looks deliberate rather than broken.

Colours are already a per-room decision in CSS; the mockups read as:
`plan` blue, `library` amber, `code` violet, `test` cyan, `ops` steel blue,
`deploy` magenta, `approval` green, `lounge` rose.

## 3. Empty rooms — what "the room" actually is

A room is **not** one flat picture. `composeRoom()` builds it at runtime from:

1. `bg-<key>.jpg` — **1600x1000**, the empty room: back wall, floor, window
2. a prop list (`roomDefs[key].items` in `app.js`) — each prop its own cut-out
   PNG, positioned and scaled by code
3. a wall screen `scr-*.png` whose content is drawn live
4. the agents, which walk in

So the deliverable per room is **an empty room plus its furniture as separate
files**, never a finished scene. A single flat painting of a furnished room
cannot be used: the agents would walk behind the desks, and the screen could
not show live text.

**The empty room must keep its middle band clear.** Agents walk across the
centre; props sit in front. Draw the wall and floor, the window, the light —
and nothing in the walking lane.

Per-room content, from the current prop lists and the mockups:

| Key | The empty room carries | Props drawn separately |
|---|---|---|
| `plan` | Pin board wall, big window, morning light | Boss desk, meeting table, calendar, plants |
| `library` | Bookshelf wall, warm wood, reading lamps | Shelves, globe, meeting table, plant stand |
| `code` | Dim focused wall, cool cast, cable tray | Three desks, monitors, cactus, plant |
| `test` | Bright clean wall, lab shelving | Desks, filing cabinet, printer, hydrangea |
| `ops` | Server-room wall, status lights, grating | Server racks, desk, printer, filing cabinet |
| `deploy` | Launch bay, high roof, hazard stripes | Desks, rug, **rocket**, bean bags, pallet, hand truck |
| `approval` | Calm reception wall, soft lamp | Reception desk, sofa, water cooler, flowers, cushion |
| `lounge` | Cosiest — warm wall, plants, low light | Rug, sofa, armchair, hammock, bean bag |

Also needed per room, unchanged from ASSET_SPEC: `sky-<key>.png` **800x500**
(what the window shows) and `plq-<key>.png` (the door plaque).

## 3b. Time of day — the code already has five phases

> Owner, 2026-11-06: backgrounds should follow the time of day —
> เช้า / สาย / บ่าย / เย็น / ดึก.

`applyPhase()` in `web/app.js` already computes exactly those five, from the
machine clock, every 30 seconds, and writes `document.body.dataset.phase`:

| Key | Thai | Hours |
|---|---|---|
| `morning` | เช้า | 06:00–08:59 |
| `late` | สาย | 09:00–11:59 |
| `afternoon` | บ่าย | 12:00–15:59 |
| `evening` | เย็น | 16:00–18:59 |
| `night` | ดึก | 19:00–05:59 |

So nothing new has to be invented — the phases, their names and their
boundaries are settled. The only question is how much art each one gets.

**Today** there is one background per room and the phase is applied as a CSS
tint, plus a sky gradient and a sun/moon. It works, it costs 8 files, and it
is why evening already looks like evening.

**Three ways to go further**, cheapest first:

| Option | Files | What you get |
|---|---|---|
| **A. Sky only** — `sky-<room>-<phase>.png`, keep one `bg-` per room | 8 x 5 = **40** small PNGs (+8 existing bg) | Real dawn/noon/dusk/night **through the window**, which is where a person actually reads the time of day. Room light still CSS-tinted |
| **B. Sky + a light pass** — A, plus a per-phase overlay PNG per room | 40 + 40 | Adds a real cast of light across the wall and floor, not a flat tint |
| **C. Full** — `bg-<room>-<phase>.jpg` | 8 x 5 = **40** backgrounds at 1600x1000 | Every surface lit correctly. Costs ~5x the background bytes and 5x the redraw work on every future room edit |

**Recommended: A.** The window is the time-of-day cue; the sky PNGs are
800x500 and small, and one wrong-looking room costs one file to fix instead of
five. Keep C in reserve for rooms where the light genuinely has to fall
differently — `lounge` at night is the obvious candidate.

Whichever is chosen, the file naming is `<kind>-<room>-<phase>.<ext>` using the
five keys above exactly, and the serving allowlist still applies
(`^[A-Za-z0-9-]{1,40}\.(png|jpg)$` — all five keys are safe within it).

A per-phase set also needs a **fallback rule**: if `sky-code-late.png` is
missing, fall back to `sky-code.png` rather than render a hole. That is a code
change, small, and it lets the set arrive one phase at a time.

## 4. Animation — and the rule that governs all of it

> **A sprite may only move because something really happened.** The project's
> standing rule is that visuals must not impersonate execution. Idle drift and
> ambient weather are decoration and are fine; anything that reads as *progress*
> must be driven by a real event from the collector.

### 4.1 Agent animation — already specified, unchanged

Six states per character, as sprite sheets. The grid is in
[ASSET_SPEC.md](ASSET_SPEC.md) and is the one place where a mistake breaks the
UI rather than merely looking wrong: **128x128 frames in a single row**,
`walkL`/`walkR` 6 frames (768x128), `work`/`think`/`rest`/`cheer` 4 frames
(512x128).

| State | Reads as | Driven by |
|---|---|---|
| `work` | Heads-down, typing | session status `running` |
| `think` | Paused, considering | running but no recent tool call |
| `rest` | Idle, breathing | status `idle` |
| `cheer` | Finished something | status `completed` |
| `walkL`/`walkR` | Moving between rooms | the room classification changed |

The redesigned characters in the mockups — pink-haired lead, silver-haired
engineer, orange-helmet tester — replace the current six roles one-for-one.
Keep all six the **same height and build** so they share a floor line.

### 4.2 Ambient animation — CSS, no art needed

These already exist and need no new files. They are decoration and may run
freely:

- sakura petals drifting (hidden at night and in a storm)
- fireflies (evening and night only)
- weather overlay — **driven by plan usage**, not random: clear / cloudy /
  storm as quota is consumed
- window sky changing with the time of day

### 4.3 What the redesign adds, and what must drive it

| Mockup element | Animation | Must be driven by |
|---|---|---|
| Neon room sign | Soft pulse while the room has an agent; dim when empty | agent count in that room |
| Room building (town) | Gentle bob on hover; lit windows when occupied | occupancy |
| Rocket in Deploy | Launch on success, fizzle on failure | **already implemented** — CI result |
| Workflow stepper | Step fills as it completes | steps actually finished |
| Central tree / fountain | Slow idle loop | nothing — pure decoration, fine |
| Session card | Status pill colour | real session status |
| Task checklist tick | Tick on completion | a real task record |

**Do not animate a transition that has no event behind it.** If the backend
cannot tell you a step finished, the step does not fill.

## 5. What is code, not art

These appear in the mockups and **do not exist in the implementation today**.
They are the real work; no amount of art delivers them.

| Mockup | Reality today | Needed |
|---|---|---|
| **Login / Token screen** | A plain token prompt exists and works | Art + layout only |
| **Home / Sessions list** — session cards, agent and task counts, grid/list toggle, search, "+ New Session" | **Does not exist.** Sessions are shown as characters inside rooms, never as a list, and there is no concept of creating one from the web | New screen, new API, and a decision about what "create a session" even means here |
| **Workflow Town** — eight buildings, one per room | The village groups houses by *where a session came from* (dispatcher / daemon / terminal), not by room. Rooms live inside a house | A different village layout and hit-testing |
| **Workflow stepper** with Plan→…→Lounge | Rooms exist; an ordered workflow with completion state does not | Backend must emit step completion |
| **Tasks & Queue, Artifacts, Logs** in the sidebar | Runs and logs exist; artifacts do not | New views; artifacts need a store |
| **"Run Plan" / "Run Coding" / "Run Tests" buttons** | Dispatch exists but only as free text into an agent pane, and only when a workspace is configured | Per-room actions mapped to real commands |
| **Sub-agent chips with an × to remove** | Sub-agents are listed **view-only, on purpose** — the web never types to a sub-agent | A security decision to re-open, not a UI change |

### 5.1 One thing in the mockups that must not ship as drawn

Three of the panels show a **progress percentage** — "72%", "58%", "86%", and a
Current Task bar at 72%.

Nothing in this system can compute that. The collector reads session files and
process state; it has no notion of how much of a task is done. A percentage
there would be invented, and both the product spec and `CONTRIBUTING.md` forbid
exactly that.

What *can* honestly be shown in that spot:

- **steps completed / total** (e.g. "3 of 8") when a workflow has ordered steps
- **elapsed time** on the current run
- **the last real event** ("Code agent is implementing… 2/5 files") — the
  mockups already show this and it is the right pattern
- **Unknown**, which the UI already has a visual state for

Use a step counter or the last event. Do not render a percentage bar unless a
real number backs it.

## 6. Suggested order

1. **Room icons** (8 SVG paths) — smallest, unblocks the stepper and signs
2. **The 36 agent sprite sheets** — without them nothing moves
3. **8 empty rooms + skies + plaques** — the rooms themselves
4. **Props** per room
5. **Town buildings** — needs the village-layout decision in §5 first
6. **Login and Home art** — needs the Sessions screen to exist first

Steps 5 and 6 are drawn against screens that do not exist yet. Commissioning
them before the code decisions in §5 are made risks art that has nowhere to go.
