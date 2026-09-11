// Labels and hover explanations for every user-facing setting — the ONE
// source both the General tab (its checkboxes) and the officer settings
// dashboard (lib/dashboard/SettingsDashboard.svelte) read, so a toggle is
// described the same way where it's flipped and where its adoption is
// charted.
//
// SETTINGS TELEMETRY (CLAUDE.md): a new Settings bool is shipped and
// aggregated automatically, but it renders on the dashboard under "Other
// settings" with its raw json tag until it gets an entry here. Put it in the
// cluster that matches where the user sees it; `linked: true` marks a
// server-stored, linked-members-only setting (the dashboard then counts it
// over linked installs and says so in the hover).

// General tab → Forwarded Messages. Each tip says which features depend on
// the box being checked — sourced from what the filter actually gates
// (filter.go) and what the server does with it.
export const FWD_OPTIONS = [
  {
    key: "guild_chat",
    label: "Guild Chat",
    tip: "Sends your guild chat to the Fuse guild stream on Discord. Raid tracking reads guild chat too — CH chain calls, TOD and batphone macros, and debuff calls all travel this way.",
  },
  // No "Guild MOTD" entry: ShouldForward never returns true for MOTD lines
  // (a /get by any member is indistinguishable from an officer setting it),
  // so the checkbox controlled nothing. See filter.go.
  {
    key: "broadcasts",
    label: "GM Broadcasts",
    tip: "Forwards GM broadcast lines so server-wide announcements reach the guild stream.",
  },
  {
    key: "server_messages",
    label: "Server Messages",
    tip: "Forwards <[SERVER MESSAGE]> lines (patch and restart notices) to the guild stream.",
  },
  {
    key: "quake_messages",
    label: "Quake Messages",
    tip: "Reports earthquakes — anchors the last-quake record and the Ring 8 window on the Timers board.",
  },
  {
    key: "engage_messages",
    label: "Engage Messages",
    tip: "Reports raid-mob engages the instant your log sees them — guild engage alerts fire from these.",
  },
  {
    key: "who_output",
    label: "/who output",
    tip: "Feeds your /who results to the shared trackers. Zone rosters, attendance logs, and character class/guild/level lookups all come from /who sightings.",
  },
  {
    key: "character_locations",
    label: "Character Locs",
    tip: "Reports your zone changes (“You have entered…”) so guildmates can see which zone your characters are in.",
  },
  {
    key: "bind_location",
    label: "Bind location",
    tip: "Reports your bind point (“You are currently bound in…”) so it shows with your character's info.",
  },
  {
    key: "slain_messages",
    label: "Slain Messages",
    tip: "Required for kill tracking: a raid mob's slain line stamps its time of death for respawn timers, clears dead add mobs from the raid card, and greys out a dead cleric in the CH chain.",
  },
  {
    key: "resist_messages",
    label: "Resist Messages",
    tip: "Forwards resist lines — required for accurate proc counting (a resisted proc still counts). Turning this off also turns off Proc Messages.",
  },
  {
    key: "proc_messages",
    label: "Proc Messages",
    tip: "Forwards weapon-proc lines so tank proc counters on the raid card stay accurate. Enabling this also enables Resist Messages (a resisted proc still counts).",
  },
  {
    key: "interrupt_messages",
    label: "Spell Interrupts",
    tip: "Reports “Your spell is interrupted.” so your CH cast bar stops on everyone's raid card the moment it happens.",
  },
  {
    key: "discipline_messages",
    label: "Disciplines",
    tip: "Reports defensive/evasive discipline use so tank disc windows show on the raid card — including for viewers who aren't in the zone.",
  },
  // These three forward no raw lines — the client aggregates locally and
  // posts numbers — but they're the categories of play being reported, so
  // they belong where a member looks to see what their client sends.
  {
    key: "melee_info",
    label: "Melee Info",
    tip: "Shares your locally-computed melee damage with the raid DPS meter.",
  },
  {
    key: "spell_info",
    label: "Spell Info",
    tip: "Shares your locally-computed spell damage with the raid DPS meter.",
  },
  {
    key: "pet_info",
    label: "Pet Info",
    tip: "Sends your pet commands to the server to associate your with your pet for DPS tracking.",
  },
  {
    key: "share_map_position",
    label: "Share Map Position",
    tip: "Required for your location to show on the map for guildmates.",
  },
  {
    key: "game_time",
    label: "Game Time (/time)",
    tip: "Forwards your /time output so the shared Norrath game clock (day/night timing) stays accurate.",
  },
  {
    key: "world_timers",
    label: "Boats & Zone Events",
    tip: "Reports boat sightings and zone-event lines you witness — they anchor the Boats & Zone Events board for the whole guild.",
  },
];

// General tab → Basic Settings.
export const BASIC_OPTIONS = [
  {
    key: "auto_start",
    label: "Start automatically",
    tip: "Launches FuseBridge when Windows starts (a Run-key entry for this user).",
  },
  {
    key: "use_middlemand",
    label: "Use middlemand",
    tip: "Login fix — runs the built-in P99 login proxy (p99-login-middlemand), filters the server list to P99, and points eqhost.txt at it. Fixes the 'server list fails to populate' login issue. Unchecking restores your original eqhost.txt.",
  },
];

// General tab → Automations (server-stored, linked members only).
export const AUTOMATION_OPTIONS = [
  {
    key: "automation_add_tracking",
    label: "Add to logs while tracking",
    tip: "Automatically add you to non-hourly raids while you're performing tracking or any other non-porter, non-idol role.",
    linked: true,
  },
  {
    key: "automation_swap_bot",
    label: "Swap toons on logs when on a bot",
    tip: "If you're playing on a bot when a raid ends, your main is added to that raid's log once the bot appears in the attendance post.",
    linked: true,
  },
  {
    key: "automation_add_missed",
    label: "Auto add to raids if missed",
    tip: "When Fuse Bridge detects you were in the zone for a non-hourly raid that closed but you got missed on the logs it will add your active toon.",
    linked: true,
  },
];

// General tab → Forwarded Messages, last row (server-stored, linked only).
export const SHARE_MAGELOS = {
  key: "share_magelos",
  label: "Share 52+ Magelos",
  tip: "List your level 52+ characters' Current gear in Fuse Shared Magelos automatically — read straight from your outputfile, so it's always the real build. Magelos you shared yourself are unaffected.",
  linked: true,
};

// Toggles that live on other tabs (Characters, Logs, Timers, Manage
// Overlays). They ride in the same snapshot; the dashboard groups them as
// "Other settings".
export const OTHER_OPTIONS = [
  {
    key: "exclude_bots",
    label: "Hide bots (Characters)",
    tip: "Characters tab: hide characters detected as bots from the list and from item searches.",
  },
  {
    key: "exclude_filtered",
    label: "Hide filtered characters",
    tip: "Characters tab: hide the characters you've filtered out of the list.",
  },
  {
    key: "archive_logs",
    label: "Archive logs",
    tip: "Logs tab → Manage Logs: move oversized, inactive eqlog files into the archive folder during quiet periods (and prune old archives if a delete age is set).",
  },
  {
    key: "audio_muted",
    label: "Trigger audio muted",
    tip: "The speaker button on the Timers tab — every trigger sound and TTS is silenced while this is on.",
  },
  {
    key: "snap_to_grid",
    label: "Snap overlays to grid",
    tip: "Manage Overlays: overlay moves and resizes snap to a 10px grid.",
  },
  {
    key: "hide_overlays_unfocused",
    label: "Hide overlays when unfocused",
    tip: "Manage Overlays: hide every overlay while a window other than EverQuest or FuseBridge is in front.",
  },
];

// Dashboard clusters, in General-tab order. `linked` on a section means
// every item is a linked-only setting.
export const SETTINGS_SECTIONS = [
  { id: "basic", title: "Basic Settings", items: BASIC_OPTIONS },
  { id: "automations", title: "Automations", items: AUTOMATION_OPTIONS, linked: true },
  { id: "forwarded", title: "Forwarded Messages", items: [...FWD_OPTIONS, SHARE_MAGELOS] },
  { id: "other", title: "Other settings", items: OTHER_OPTIONS },
];

// Map tab + map overlay settings (frontend-only prefs pushed into the
// snapshot through SetOverlayPrefs). Keys match the server's map.bools.
export const MAP_OPTIONS = [
  { key: "trail", label: "Show Trail", tip: "Map tab: draw the path your character has walked this session." },
  { key: "focus_level", label: "Focus Current Level", tip: "Map tab: fade map lines that belong to other floors/levels of the zone." },
  { key: "grid_always", label: "Coordinate grid (always)", tip: "Map tab: the loc-space grid switched on permanently rather than just for this session." },
  { key: "autohide", label: "Auto-hide overlay", tip: "Map overlay: slide up to just the title bar after a minute with no /loc, and back down on the next one." },
  { key: "aot", label: "Overlay always on top", tip: "Map overlay: kept above the game window." },
  { key: "stay_unlocked", label: "Stay unlocked", tip: "Map overlay: keeps the map interactive (pan/zoom/search) when Lock Overlays is pressed on the Timers tab." },
  { key: "overlay_open", label: "Map overlay open", tip: "The map overlay was open (or set to reopen at launch) when the snapshot was taken." },
];
