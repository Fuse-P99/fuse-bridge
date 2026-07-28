<script>
  // Bulk quest import from a scraped JSON file (see questseed/ in the repo).
  //
  // The file speaks NAMES, because whoever produced it can't know this
  // database's item_ids, mob_ids or zone_ids. Everything is resolved here
  // against the live DB, and the dry run reports what wouldn't resolve BEFORE
  // anything is written — which matters most for items, since the server fails
  // a whole quest on one unknown name and quest components are exactly the
  // obscure items least likely to have been scraped yet.
  //
  // Saving goes through SaveQuest like any hand edit, so every validation rule
  // applies identically and a malformed file can't get past them.
  import {
    LookupItems,
    ListQuests,
    ListQuestZones,
    SearchQuestMobs,
    SaveQuest,
    SaveQuestMob,
    StubItems,
  } from "../../bindings/FuseBridge/app.js";

  export let onClose;
  export let onImported; // called after a successful apply so the list reloads

  // pick → checked → applying → done
  let phase = "pick";
  let fileName = "";
  let seed = null;
  let err = "";
  let progress = "";
  let report = null;
  let log = [];
  let stubbing = false;
  let stubErr = "";

  // ── export ─────────────────────────────────────────────────────────────────
  // Dumps what's actually stored, in the same shape the importer reads, so a
  // round trip is lossless and the file can be diffed against the seed that
  // produced it. This is the only way to see the stored result from outside the
  // app — worth having when a field looks empty and the question is whether it
  // failed to import or failed to save.

  function toSeed(q) {
    return {
      name: q.name,
      class: q.class || undefined,
      wiki_url: q.wiki_url || undefined,
      prereqs: (q.prereqs || []).map((p) => p.name),
      rewards: (q.rewards || []).map((r) =>
        r.kind === "faction"
          ? { kind: "faction", group: r.faction_group, delta: r.faction_delta }
          : r.kind === "cycle"
            ? { kind: "cycle", cycle: r.cycle || [] }
            : { kind: "item", name: r.name },
      ),
      steps: (q.steps || []).map((s) => {
        const ins = (s.items || []).filter((i) => i.role !== "out");
        const outs = (s.items || []).filter((i) => i.role === "out");
        const o = { kind: s.kind };
        if (s.mobs && s.mobs.length)
          o.mobs = s.mobs.map((m) => {
            const e = { name: m.name, zone: m.zone || "" };
            // EQ /loc order, Y then X. Absent means unrecorded, which is
            // deliberate for wanderers — see the map-marker rule.
            if (m.has_loc) e.loc = [m.loc_y, m.loc_x];
            return e;
          });
        if (s.zone_name || s.zone_id) o.zone = s.zone_name || s.zone_id;
        if (s.has_loc) o.loc = [s.loc_y, s.loc_x];
        if (s.tradeskill) o.tradeskill = s.tradeskill;
        if (s.skill_req) o.skill = s.skill_req;
        if (s.method) o.method = s.method;
        if (s.say) o.say = s.say;
        if (s.faction_level)
          o.faction = { level: s.faction_level, group: s.faction_group };
        if (s.plat_cost) o.plat = s.plat_cost;
        if (s.follows) o.follows = true;
        if (ins.length)
          o.items_in = ins.map((i) => {
            const e = { name: i.name };
            if (i.alts && i.alts.length) e.alts = i.alts;
            if (i.consumed_ok === false) e.consumed = false;
            if (i.consumed_fail === false) e.consumed_fail = false;
            return e;
          });
        if (outs.length) o.items_out = outs.map((i) => i.name);
        if (s.note) o.note = s.note;
        return o;
      }),
    };
  }

  async function exportAll() {
    progress = "Exporting…";
    err = "";
    try {
      const all = (await ListQuests()) || [];
      const blob = new Blob(
        [
          JSON.stringify(
            { version: 1, exported: new Date().toISOString(), quests: all.map(toSeed) },
            null,
            2,
          ),
        ],
        { type: "application/json" },
      );
      const a = document.createElement("a");
      a.href = URL.createObjectURL(blob);
      a.download = "quests-export.json";
      a.click();
      URL.revokeObjectURL(a.href);
      progress = `Exported ${all.length} quest${all.length === 1 ? "" : "s"}.`;
    } catch (e) {
      err = String(e);
      progress = "";
    }
  }

  // ── reading ────────────────────────────────────────────────────────────────

  async function onFile(e) {
    const f = e.currentTarget.files && e.currentTarget.files[0];
    if (!f) return;
    err = "";
    fileName = f.name;
    try {
      const parsed = JSON.parse(await f.text());
      const list = Array.isArray(parsed) ? parsed : parsed.quests;
      if (!Array.isArray(list)) throw new Error("no 'quests' array");
      seed = list.filter((q) => q && q.name);
      if (!seed.length) throw new Error("no quests with a name");
      await check();
    } catch (e2) {
      seed = null;
      err = `Couldn't read ${f.name}: ${e2.message || e2}`;
    }
  }

  // Every item name the file mentions, in one flat list.
  function itemNames(q) {
    const out = [];
    for (const s of q.steps || []) {
      for (const i of s.items_in || []) out.push(i.name, ...(i.alts || []));
      for (const o of s.items_out || []) out.push(o);
    }
    for (const r of q.rewards || []) {
      if (r.kind === "cycle") out.push(...(r.cycle || []));
      else if (r.kind !== "faction") out.push(r.name);
    }
    return out.filter((n) => n && n.trim());
  }

  function mobRefs(q) {
    const out = [];
    for (const s of q.steps || [])
      for (const m of s.mobs || []) if (m && m.name) out.push(m);
    return out;
  }

  const lc = (s) => (s || "").trim().toLowerCase();

  // ── dry run ────────────────────────────────────────────────────────────────

  async function check() {
    phase = "checking";
    err = "";
    log = [];
    const r = {
      quests: seed.length,
      missingItems: [],
      stubItems: [],
      newMobs: [],
      nearMobs: [],
      ambiguousMobs: [],
      unknownZones: [],
      replaces: [],
    };
    try {
      // Items in one round trip. This also queues anything unknown for a wiki
      // scrape server-side, so reporting the shortfall starts fixing it.
      progress = "Checking items…";
      const names = [...new Set(seed.flatMap(itemNames))];
      const look = await LookupItems(names);
      const have = new Set(Object.keys(look.items || {}).map(lc));
      r.missingItems = names.filter((n) => !have.has(lc(n))).sort();
      // Placeholders satisfy the foreign key, so they are not missing — but
      // they carry no stats and price at nothing, which is worth saying out
      // loud rather than letting them look like ordinary items.
      r.stubItems = names
        .filter((n) => (look.items || {})[lc(n)]?.stub)
        .sort();

      // Zones, against name, id AND nicknames. The wiki calls zones whatever
      // players call them — "The Hole", "Sol A" — and eqzones carries those as
      // nicknames, so matching the canonical name alone rejects zones that are
      // plainly there. Canonical names win over nicknames where both match.
      progress = "Checking zones…";
      const zones = (await ListQuestZones()) || [];
      const byZoneName = new Map();
      const put = (k, id) => {
        if (k && !byZoneName.has(k)) byZoneName.set(k, id);
      };
      for (const z of zones) put(lc(z.name), z.id);
      for (const z of zones) put(lc(z.id), z.id);
      for (const z of zones) for (const n of z.nicks || []) put(lc(n), z.id);

      const wanted = new Set();
      for (const q of seed) {
        for (const s of q.steps || []) {
          if (s.zone) wanted.add(s.zone);
          for (const m of s.mobs || []) if (m.zone) wanted.add(m.zone);
        }
      }
      r.unknownZones = [...wanted].filter((z) => !byZoneName.has(lc(z))).sort();
      r.zoneIds = byZoneName;

      // Mobs, one search per distinct name. Matches are narrowed by zone when
      // the file gives one, because EQ reuses NPC names across zones.
      progress = "Checking NPCs…";
      const refs = new Map();
      for (const q of seed)
        for (const m of mobRefs(q)) refs.set(`${lc(m.name)}|${lc(m.zone)}`, m);
      r.mobIds = new Map();
      for (const [key, m] of refs) {
        let found = (await SearchQuestMobs(m.name)) || [];
        // The search prefix-matches, so "A Fire Sprite" never finds a row
        // stored as "Fire Sprite". EQ is inconsistent about the leading
        // article and the wiki more so, so try again without it rather than
        // creating a near-duplicate of a mob that's already there.
        const bare = m.name.replace(/^(a|an|the)\s+/i, "");
        if (!found.length && bare !== m.name) {
          found = (await SearchQuestMobs(bare)) || [];
        }
        const exact = found.filter(
          (x) => lc(x.name) === lc(m.name) || lc(x.name) === lc(bare),
        );
        // Compare on the resolved zone id, so a mob recorded under the
        // canonical zone still matches a file that used a nickname.
        const wantZone = m.zone ? byZoneName.get(lc(m.zone)) || "" : "";
        const inZone = wantZone
          ? exact.filter((x) => lc(x.zone_id) === lc(wantZone))
          : exact;
        const pick = inZone.length ? inZone : exact;
        if (pick.length === 1) r.mobIds.set(key, pick[0].id);
        else if (pick.length > 1) {
          // Two NPCs of the same name in the same zone is real — the Monk
          // epic's mad and sane Kaiaren — and the file can't tell them apart.
          r.ambiguousMobs.push(`${m.name}${m.zone ? ` (${m.zone})` : ""}`);
          r.mobIds.set(key, pick[0].id);
        } else {
          r.newMobs.push(m);
          // Something similar exists but isn't an exact name match. Usually a
          // spelling difference between the wiki and eqmobs, and creating a
          // second row would leave two NPCs that are really one.
          if (found.length) {
            r.nearMobs.push({
              want: `${m.name}${m.zone ? ` (${m.zone})` : ""}`,
              near: found
                .slice(0, 3)
                .map((x) => x.name + (x.zone_name ? ` (${x.zone_name})` : ""))
                .join(", "),
            });
          }
        }
      }

      // Quests already present under the same name will be REPLACED, not
      // duplicated — including any you entered by hand.
      progress = "Checking quests…";
      const existing = (await ListQuests()) || [];
      r.existingIds = new Map(existing.map((q) => [lc(q.name), q.id]));
      r.replaces = seed
        .filter((q) => r.existingIds.has(lc(q.name)))
        .map((q) => q.name);

      report = r;
      phase = "checked";
    } catch (e) {
      err = String(e);
      phase = "pick";
    }
    progress = "";
  }

  // Create name-only rows for the items still missing, then re-run the dry run
  // so the report reflects them. Deliberately a separate button rather than
  // part of apply: a name that IS on the wiki should be scraped properly, and
  // stubbing it would settle for less permanently.
  async function makeStubs() {
    if (!report || !report.missingItems.length) return;
    stubbing = true;
    stubErr = "";
    try {
      const res = await StubItems(report.missingItems);
      const bad = Object.keys(res.failed || {});
      if (bad.length) {
        stubErr = `${bad.length} could not be created: ${bad
          .slice(0, 5)
          .join(", ")}`;
      }
      await check();
    } catch (e) {
      stubErr = String(e);
    }
    stubbing = false;
  }

  // ── apply ──────────────────────────────────────────────────────────────────

  function stepPayload(q, s, r) {
    const zoneOf = (z) => (z ? r.zoneIds.get(lc(z)) || "" : "");
    const loc = Array.isArray(s.loc) ? s.loc : null;
    return {
      kind: s.kind || "handin",
      tradeskill: s.tradeskill || "",
      skill_req: s.skill || 0,
      // A seed written before acquire absorbed the ground-spawn kind carries
      // kind "ground" and no method. The server aliases it rather than
      // rejecting the file; sending it through untouched is what lets it.
      method: s.method || "",
      mobs: (s.mobs || [])
        .map((m) => ({
          id: r.mobIds.get(`${lc(m.name)}|${lc(m.zone)}`) || 0,
          name: m.name,
          zone: m.zone || "",
        }))
        .filter((m) => m.id),
      zone_id: zoneOf(s.zone),
      // EQ /loc order in the file: [y, x].
      loc_y: loc ? Number(loc[0]) || 0 : 0,
      loc_x: loc ? Number(loc[1]) || 0 : 0,
      has_loc: !!loc,
      say: s.say || "",
      faction_level: (s.faction && s.faction.level) || "",
      faction_group: (s.faction && s.faction.group) || "",
      plat_cost: s.plat || 0,
      follows: !!s.follows,
      note: s.note || "",
      items: [
        ...(s.items_in || []).map((i) => ({
          name: i.name,
          alts: i.alts || [],
          role: "in",
          // Both default to true: an item handed over is normally gone, and a
          // failed combine normally eats it.
          consumed_ok: i.consumed !== false,
          consumed_fail: i.consumed_fail !== false,
        })),
        ...(s.items_out || []).map((n) => ({
          name: n,
          alts: [],
          role: "out",
          consumed_ok: true,
          consumed_fail: true,
        })),
      ],
    };
  }

  function questPayload(q, r, prereqs) {
    return {
      id: r.existingIds.get(lc(q.name)) || 0,
      name: q.name,
      class: q.class || "",
      wiki_url: q.wiki_url || "",
      prereqs,
      rewards: (q.rewards || []).map((rw) => ({
        kind: rw.kind || "item",
        name: rw.name || "",
        faction_group: rw.group || rw.faction_group || "",
        faction_delta: rw.delta || rw.faction_delta || 0,
        cycle: rw.cycle || [],
      })),
      steps: (q.steps || []).map((s) => stepPayload(q, s, r)),
    };
  }

  async function apply() {
    phase = "applying";
    err = "";
    log = [];
    const r = report;
    let ok = 0;
    try {
      // NPCs the file names that aren't in the mob DB. confirm:true because
      // the dry run already showed you the same-name collisions.
      for (const m of r.newMobs) {
        progress = `Adding NPC ${m.name}…`;
        const loc = Array.isArray(m.loc) ? m.loc : null;
        const res = await SaveQuestMob(
          {
            id: 0,
            name: m.name,
            nicknames: "",
            zone_id: r.zoneIds.get(lc(m.zone)) || "",
            faction: "",
            quest_mob: true,
            // Absent for wanderers and mob types with many spawn points; the
            // map overlay is suppressed without it rather than guessing.
            loc_y: loc ? Number(loc[0]) || 0 : 0,
            loc_x: loc ? Number(loc[1]) || 0 : 0,
            has_loc: !!loc,
          },
          true,
        );
        if (res && res.mob && res.mob.id) {
          r.mobIds.set(`${lc(m.name)}|${lc(m.zone)}`, res.mob.id);
        }
      }

      // Pass one: every quest, without prerequisites — they can point at a
      // quest this same file hasn't created yet.
      for (const q of seed) {
        progress = `Saving ${q.name}…`;
        try {
          await SaveQuest(questPayload(q, r, []));
          ok++;
        } catch (e) {
          log = [...log, `${q.name}: ${e}`];
        }
      }

      // Pass two: only quests that actually have prerequisites, now that every
      // name in the file resolves. One unknown prerequisite is reported and
      // skipped rather than failing the quest.
      const after = (await ListQuests()) || [];
      const ids = new Map(after.map((x) => [lc(x.name), x.id]));
      for (const q of seed) {
        const want = (q.prereqs || []).filter(Boolean);
        if (!want.length) continue;
        const found = want.filter((n) => ids.has(lc(n)));
        for (const n of want)
          if (!ids.has(lc(n)))
            log = [...log, `${q.name}: prerequisite "${n}" not found — skipped`];
        if (!found.length) continue;
        progress = `Linking ${q.name}…`;
        r.existingIds = ids;
        try {
          await SaveQuest(
            questPayload(
              q,
              r,
              found.map((n) => ({ id: ids.get(lc(n)), name: n })),
            ),
          );
        } catch (e) {
          log = [...log, `${q.name} (prerequisites): ${e}`];
        }
      }

      progress = "";
      report = { ...r, saved: ok };
      phase = "done";
      onImported && onImported();
    } catch (e) {
      err = String(e);
      phase = "checked";
      progress = "";
    }
  }
</script>

<!-- svelte-ignore a11y-click-events-have-key-events a11y-no-static-element-interactions -->
<div class="q-ov q-ov-top" on:click|self={() => onClose && onClose()}>
  <div class="q-dlg">
    <div class="q-title">Import Quests</div>

    {#if phase === "pick" || phase === "checking"}
      <div class="q-note">
        Load a scraped quest file. Everything in it is resolved against this
        database first and nothing is written until you say so.
      </div>
      <input
        class="q-in"
        type="file"
        accept=".json,application/json"
        on:change={onFile}
      />
      {#if progress}<div class="q-note">{progress}</div>{/if}
      {#if err}<div class="q-err">{err}</div>{/if}
    {:else}
      <div class="q-note">
        {fileName} — {report.quests} quest{report.quests === 1 ? "" : "s"}
      </div>

      {#if report.missingItems.length}
        <!-- The blocker. SaveQuest rejects a whole quest on one unknown item,
             so these have to exist first. LookupItems has already queued them
             for a wiki scrape; the rest may need the Add Item dialog. -->
        <div class="q-block q-bad">
          <div class="q-blockhead">
            {report.missingItems.length} item{report.missingItems.length === 1
              ? ""
              : "s"} not in the item DB
          </div>
          <div class="q-blockbody">{report.missingItems.join(", ")}</div>
          <div class="q-note">
            Quests using these will fail. They've been queued for a wiki scrape
            — reopen the Magelo tab shortly, then run this again.
          </div>
          <!-- Not every quest component has a wiki page, and waiting on a
               scrape that will never succeed blocks the import for good. A
               placeholder is name and wiki link only; a real scrape landing
               later fills it in and clears the flag. -->
          <div class="q-blockact">
            <button class="q-btn" disabled={stubbing} on:click={makeStubs}>
              {stubbing
                ? "Creating…"
                : `Create ${report.missingItems.length} placeholder${report.missingItems.length === 1 ? "" : "s"}`}
            </button>
            <span class="q-note"
              >Use this for the ones the wiki has no page for. The link will
              404 until someone writes it.</span
            >
          </div>
          {#if stubErr}<div class="q-err">{stubErr}</div>{/if}
        </div>
      {/if}

      {#if report.stubItems.length}
        <!-- Placeholders already in the DB. These import fine — they exist, so
             the foreign key is satisfied — but they carry no stats, so say so
             rather than letting them pass silently as known items. -->
        <div class="q-block">
          <div class="q-blockhead">
            {report.stubItems.length} placeholder item{report.stubItems
              .length === 1
              ? ""
              : "s"}
          </div>
          <div class="q-blockbody">{report.stubItems.join(", ")}</div>
          <div class="q-note">
            Name and wiki link only — no stats, and no DKP value in any quest
            total that includes them.
          </div>
        </div>
      {/if}

      {#if report.unknownZones.length}
        <div class="q-block q-bad">
          <div class="q-blockhead">
            {report.unknownZones.length} unknown zone{report.unknownZones
              .length === 1
              ? ""
              : "s"}
          </div>
          <div class="q-blockbody">{report.unknownZones.join(", ")}</div>
          <div class="q-note">These steps will save without a zone.</div>
        </div>
      {/if}

      {#if report.newMobs.length}
        <div class="q-block">
          <div class="q-blockhead">
            {report.newMobs.length} NPC{report.newMobs.length === 1 ? "" : "s"} will
            be created
          </div>
          <div class="q-blockbody">
            {report.newMobs.map((m) => m.name).join(", ")}
          </div>
        </div>
      {/if}

      {#if report.nearMobs.length}
        <!-- Worth a look before creating: an NPC that already exists under a
             slightly different name would end up duplicated, and the parser
             and raid tracker read the same table. -->
        <div class="q-block q-warnblock">
          <div class="q-blockhead">
            {report.nearMobs.length} NPC{report.nearMobs.length === 1 ? "" : "s"} resemble
            something already in the mob DB
          </div>
          <div class="q-blockbody">
            {#each report.nearMobs as n}
              <div>{n.want} → {n.near}</div>
            {/each}
          </div>
          <div class="q-note">
            These will be created as new. If one is really the same NPC spelled
            differently, fix the file or rename the existing row first.
          </div>
        </div>
      {/if}

      {#if report.ambiguousMobs.length}
        <div class="q-block q-warnblock">
          <div class="q-blockhead">
            {report.ambiguousMobs.length} ambiguous NPC name{report.ambiguousMobs
              .length === 1
              ? ""
              : "s"}
          </div>
          <div class="q-blockbody">{report.ambiguousMobs.join(", ")}</div>
          <div class="q-note">
            More than one NPC matches. The first is used — check these
            afterwards, since some quests turn handing to the wrong one into a
            lost item.
          </div>
        </div>
      {/if}

      {#if report.replaces.length}
        <div class="q-block q-warnblock">
          <div class="q-blockhead">
            {report.replaces.length} existing quest{report.replaces.length === 1
              ? ""
              : "s"} will be REPLACED
          </div>
          <div class="q-blockbody">{report.replaces.join(", ")}</div>
          <div class="q-note">
            Matched by name. Any hand edits to these are overwritten.
          </div>
        </div>
      {/if}

      {#if log.length}
        <div class="q-block q-bad">
          <div class="q-blockhead">{log.length} problem{log.length === 1 ? "" : "s"}</div>
          <div class="q-blockbody">
            {#each log as l}<div>{l}</div>{/each}
          </div>
        </div>
      {/if}

      {#if phase === "done"}
        <div class="q-note q-ok">
          Imported {report.saved} of {report.quests} quests.
        </div>
      {/if}
      {#if progress}<div class="q-note">{progress}</div>{/if}
      {#if err}<div class="q-err">{err}</div>{/if}
    {/if}

    <div class="q-btns">
      <!-- Always available: exporting is how you inspect what actually got
           stored, which matters most right after an import. -->
      <button class="q-btn" on:click={exportAll}>Export all…</button>
      <span class="q-spacer"></span>
      <button class="q-btn" on:click={() => onClose && onClose()}
        >{phase === "done" ? "Close" : "Cancel"}</button
      >
      {#if phase === "checked"}
        <button class="q-btn q-go" on:click={apply}>
          Import {report.quests - report.replaces.length} new{report.replaces
            .length
            ? `, replace ${report.replaces.length}`
            : ""}
        </button>
      {/if}
    </div>
  </div>
</div>

<style>
  .q-ov {
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.55);
    display: flex;
    align-items: center;
    justify-content: center;
  }
  .q-ov-top {
    z-index: 210;
  }
  .q-dlg {
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    border-radius: 8px;
    padding: 16px;
    width: 560px;
    max-width: 94vw;
    max-height: 88vh;
    overflow-y: auto;
    display: flex;
    flex-direction: column;
    gap: 9px;
  }
  .q-title {
    font-size: 14px;
    font-weight: 700;
    color: var(--text-primary);
  }
  .q-note {
    font-size: 11.5px;
    color: var(--text-muted);
    line-height: 1.5;
  }
  .q-ok {
    color: var(--accent);
  }
  .q-err {
    font-size: 12px;
    color: #ff6b6b;
  }
  .q-in {
    background: var(--bg-input);
    color: var(--text-primary);
    border: 1px solid var(--border);
    border-radius: 5px;
    padding: 6px 8px;
    font-size: 13px;
  }
  /* One block per class of finding, so the blocking ones read as blocking. */
  .q-block {
    background: var(--bg-panel);
    border: 1px solid var(--border);
    border-radius: 5px;
    padding: 7px 9px;
    display: flex;
    flex-direction: column;
    gap: 3px;
  }
  .q-bad {
    border-color: #a04a4a;
  }
  .q-warnblock {
    border-color: var(--accent-dim);
  }
  .q-blockhead {
    font-size: 12px;
    font-weight: 600;
    color: var(--text-primary);
  }
  .q-blockbody {
    font-size: 11px;
    color: var(--text-secondary);
    max-height: 130px;
    overflow-y: auto;
    word-break: break-word;
    line-height: 1.5;
  }
  /* Action offered inside a report block, with its caveat beside it. Wraps so
     the explanation drops under the button in a narrow dialog rather than
     squeezing it. */
  .q-blockact {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 8px;
    margin-top: 2px;
  }
  .q-btns {
    display: flex;
    align-items: center;
    gap: 8px;
  }
  /* Export sits apart from the import actions — it's an inspection tool, not a
     step in the flow. */
  .q-spacer {
    flex: 1;
  }
  .q-btn {
    background: var(--bg-panel);
    border: 1px solid var(--border);
    color: var(--text-secondary);
    border-radius: 4px;
    padding: 6px 14px;
    font-size: 12px;
    cursor: pointer;
    white-space: nowrap;
    transition:
      background 0.15s,
      border-color 0.15s,
      color 0.15s;
  }
  .q-btn:hover {
    background: var(--bg-input);
    border-color: var(--border-hover);
    color: var(--text-primary);
  }
  .q-go {
    border-color: var(--accent);
    color: var(--accent);
  }
</style>
