// Guild alliances the client clusters for display — shared by the Zones tab
// and the raid card's Competition block so both agree on who counts as one
// force.

// The Sanctum alliance: these guilds partner with one another to compete
// with Fuse, so the listing clusters them under one pseudo-guild ("Sanctum")
// — the competition's real size at a glance. Individual rows under the
// classes still carry each character's own guild tag.
export const SANCTUM_ALLIANCE = new Set(
  [
    "Castle",
    "Misfits of Marr",
    "The Sticky Bandits",
    "Ex Astra",
    "Homeland",
    "Rivervale Vanguard",
    "Sanctum",
    "Stream Team",
    "Auld Lang Syne",
    "Dawn Believers",
    "Europa",
  ].map((g) => g.toLowerCase()),
);
export const allianceGuild = (g) => !!g && SANCTUM_ALLIANCE.has(g.toLowerCase());
