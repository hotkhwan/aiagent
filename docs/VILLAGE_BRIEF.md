# Village brief — the map outside the house

> **Status, 2026-11-09.** Two changes, in this order.
>
> **The map is one painting**, buildings and all — the owner's own design
> (`docs/incoming/ChatGPT Image Nov 7, 2026, 11_13_14 PM-3.png`, resized to
> 1660x940, JPEG q70). No house sprites, no prop sprites: the painting has its
> own trees, lamps, railings, water and fountain, and the pastel set that used
> to stand on top was a second copy of all of it in the wrong style.
>
> **The village is one house.** Houses were tried per session, then per
> project, and both ran into the same wall: sessions are unbounded and the map
> has five buildings. The owner settled it — *"list เป็นหลัก, หมู่บ้านเหลือหลังเดียว"*.
> So the **session list is the home screen** (`renderList`, it holds any
> number), and the map is a place you enter: one hit box on the lit plaza in
> the middle (`ENTRANCE` in `web/app.js`), one card, and everyone is inside.
>
> The five painted buildings are now scenery. Nothing in the code points at
> them, so a replacement map does not have to have five of anything.

## What the village is

Clicking the plaza sets `st.house` and `renderInterior` draws the eight rooms
with **every live agent on the box** in them, placed by what they are doing —
the main agents, the sub-agents they spawned, and the agcmd runs waiting on a
decision. A session is a main agent; its sub-agents are shown beside it and are
view-only, as they have always been.

Empty is a real state: with nothing live, the card reads `ไม่มี agent` and the
rooms are empty. Nothing is dimmed or hidden any more, because there is no
longer a per-house occupancy to show.

---

## If the map is ever redrawn

The map is the only file the village needs. What a replacement has to be:

- **1660x940**, JPEG, the whole scene in one image — sky, ground, buildings,
  trees, lamps, water.
- **Night, neon, sakura**, the same world as the rooms.
  `docs/audit/interior-neon.jpg` is the inside of one of these buildings.
- **No text on the signs.** Any lettering is baked in and would be wrong in
  the other language.
- **A clear middle**, lit or otherwise obviously the way in. That patch is the
  only thing the code places: one hit box, `ENTRANCE` in `web/app.js`.

Install it, then re-measure:

```sh
sips -s format jpeg -s formatOptions 70 -z 940 1660 <src> --out web/assets/bg-village.jpg
go test ./...
```

Then open the village and check that `ENTRANCE` still sits on the way in, and
move it if it does not. There is no tool for this; it is done by looking.

The sky, the stars, the sun and moon, the falling petals, the fireflies and the
weather tint are drawn by the code on top of the painting, and need no art.
