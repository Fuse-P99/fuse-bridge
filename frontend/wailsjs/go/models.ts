export namespace main {
	
	export class BatphoneBanner {
	    text: string;
	    sent_at: number;
	
	    static createFrom(source: any = {}) {
	        return new BatphoneBanner(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.text = source["text"];
	        this.sent_at = source["sent_at"];
	    }
	}
	export class CharEntry {
	    name: string;
	    match_count: number;
	    is_bot: boolean;
	    is_filtered: boolean;
	
	    static createFrom(source: any = {}) {
	        return new CharEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.match_count = source["match_count"];
	        this.is_bot = source["is_bot"];
	        this.is_filtered = source["is_filtered"];
	    }
	}
	export class CharTableRow {
	    name: string;
	    level: number;
	    class: string;
	    race: string;
	    zone: string;
	    zone_updated: number;
	    bind: string;
	    bind_updated: number;
	    keys: Record<string, boolean>;
	
	    static createFrom(source: any = {}) {
	        return new CharTableRow(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.level = source["level"];
	        this.class = source["class"];
	        this.race = source["race"];
	        this.zone = source["zone"];
	        this.zone_updated = source["zone_updated"];
	        this.bind = source["bind"];
	        this.bind_updated = source["bind_updated"];
	        this.keys = source["keys"];
	    }
	}
	export class InventoryItem {
	    location: string;
	    name: string;
	    count: number;
	
	    static createFrom(source: any = {}) {
	        return new InventoryItem(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.location = source["location"];
	        this.name = source["name"];
	        this.count = source["count"];
	    }
	}
	export class MapPosition {
	    name: string;
	    zone: string;
	    x: number;
	    y: number;
	    z: number;
	    heading: number;
	
	    static createFrom(source: any = {}) {
	        return new MapPosition(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.zone = source["zone"];
	        this.x = source["x"];
	        this.y = source["y"];
	        this.z = source["z"];
	        this.heading = source["heading"];
	    }
	}
	export class PlayerPosition {
	    zone: string;
	    x: number;
	    y: number;
	    z: number;
	    heading: number;
	    time: number;
	
	    static createFrom(source: any = {}) {
	        return new PlayerPosition(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.zone = source["zone"];
	        this.x = source["x"];
	        this.y = source["y"];
	        this.z = source["z"];
	        this.heading = source["heading"];
	        this.time = source["time"];
	    }
	}
	export class RaidCHSlot {
	    label: string;
	    cleric: string;
	    tank: string;
	    dead: boolean;
	
	    static createFrom(source: any = {}) {
	        return new RaidCHSlot(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.label = source["label"];
	        this.cleric = source["cleric"];
	        this.tank = source["tank"];
	        this.dead = source["dead"];
	    }
	}
	export class RaidCurrentTarget {
	    name: string;
	    debuffs: RaidKV[];
	    sieve: number;
	
	    static createFrom(source: any = {}) {
	        return new RaidCurrentTarget(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.debuffs = this.convertValues(source["debuffs"], RaidKV);
	        this.sieve = source["sieve"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class RaidRaider {
	    name: string;
	    discord: string;
	    level: number;
	
	    static createFrom(source: any = {}) {
	        return new RaidRaider(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.discord = source["discord"];
	        this.level = source["level"];
	    }
	}
	export class RaidClassGroup {
	    class: string;
	    members: RaidRaider[];
	
	    static createFrom(source: any = {}) {
	        return new RaidClassGroup(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.class = source["class"];
	        this.members = this.convertValues(source["members"], RaidRaider);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class RaidRaiders {
	    total: number;
	    groups: RaidClassGroup[];
	
	    static createFrom(source: any = {}) {
	        return new RaidRaiders(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.total = source["total"];
	        this.groups = this.convertValues(source["groups"], RaidClassGroup);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class RaidLoot {
	    name: string;
	    wiki_url: string;
	    price: string;
	
	    static createFrom(source: any = {}) {
	        return new RaidLoot(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.wiki_url = source["wiki_url"];
	        this.price = source["price"];
	    }
	}
	export class RaidKV {
	    name: string;
	    value: string;
	
	    static createFrom(source: any = {}) {
	        return new RaidKV(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.value = source["value"];
	    }
	}
	export class RaidCard {
	    target: string;
	    status: string;
	    killed_ago: string;
	    target_hp: number;
	    active_main_tank: string;
	    active_ramp_tank: string;
	    main_tank_list: string;
	    trash_tank_list: string;
	    rampage_tank_list: string;
	    bump_list: string;
	    fluffer_clerics: string;
	    debuffs: RaidKV[];
	    ch_chain: RaidCHSlot[];
	    loot: RaidLoot[];
	    raiders: RaidRaiders;
	    discord_channel_id: string;
	    discord_url: string;
	    sieve: number;
	    current_targets: RaidCurrentTarget[];
	    current_tanks: string[];
	    tank_procs: Record<string, number>;
	
	    static createFrom(source: any = {}) {
	        return new RaidCard(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.target = source["target"];
	        this.status = source["status"];
	        this.killed_ago = source["killed_ago"];
	        this.target_hp = source["target_hp"];
	        this.active_main_tank = source["active_main_tank"];
	        this.active_ramp_tank = source["active_ramp_tank"];
	        this.main_tank_list = source["main_tank_list"];
	        this.trash_tank_list = source["trash_tank_list"];
	        this.rampage_tank_list = source["rampage_tank_list"];
	        this.bump_list = source["bump_list"];
	        this.fluffer_clerics = source["fluffer_clerics"];
	        this.debuffs = this.convertValues(source["debuffs"], RaidKV);
	        this.ch_chain = this.convertValues(source["ch_chain"], RaidCHSlot);
	        this.loot = this.convertValues(source["loot"], RaidLoot);
	        this.raiders = this.convertValues(source["raiders"], RaidRaiders);
	        this.discord_channel_id = source["discord_channel_id"];
	        this.discord_url = source["discord_url"];
	        this.sieve = source["sieve"];
	        this.current_targets = this.convertValues(source["current_targets"], RaidCurrentTarget);
	        this.current_tanks = source["current_tanks"];
	        this.tank_procs = source["tank_procs"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	
	
	
	
	
	export class Settings {
	    guild_chat: boolean;
	    guild_motd: boolean;
	    broadcasts: boolean;
	    server_messages: boolean;
	    quake_messages: boolean;
	    engage_messages: boolean;
	    who_output: boolean;
	    character_locations: boolean;
	    bind_location: boolean;
	    share_map_position: boolean;
	    exclude_bots: boolean;
	    exclude_filtered: boolean;
	    startup_configured: boolean;
	    use_middlemand: boolean;
	    eq_directory: string;
	    dev_mode_fuse_rocks: boolean;
	    slain_messages: boolean;
	    resist_messages: boolean;
	    proc_messages: boolean;
	    token: string;
	
	    static createFrom(source: any = {}) {
	        return new Settings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.guild_chat = source["guild_chat"];
	        this.guild_motd = source["guild_motd"];
	        this.broadcasts = source["broadcasts"];
	        this.server_messages = source["server_messages"];
	        this.quake_messages = source["quake_messages"];
	        this.engage_messages = source["engage_messages"];
	        this.who_output = source["who_output"];
	        this.character_locations = source["character_locations"];
	        this.bind_location = source["bind_location"];
	        this.share_map_position = source["share_map_position"];
	        this.exclude_bots = source["exclude_bots"];
	        this.exclude_filtered = source["exclude_filtered"];
	        this.startup_configured = source["startup_configured"];
	        this.use_middlemand = source["use_middlemand"];
	        this.eq_directory = source["eq_directory"];
	        this.dev_mode_fuse_rocks = source["dev_mode_fuse_rocks"];
	        this.slain_messages = source["slain_messages"];
	        this.resist_messages = source["resist_messages"];
	        this.proc_messages = source["proc_messages"];
	        this.token = source["token"];
	    }
	}
	export class SpellClassEntry {
	    class: string;
	    level: number;
	
	    static createFrom(source: any = {}) {
	        return new SpellClassEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.class = source["class"];
	        this.level = source["level"];
	    }
	}
	export class SpellEntry {
	    name: string;
	    level: number;
	    mana: number;
	    cast_time: string;
	    wiki_url: string;
	    description: string;
	    spell_type: string;
	
	    static createFrom(source: any = {}) {
	        return new SpellEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.level = source["level"];
	        this.mana = source["mana"];
	        this.cast_time = source["cast_time"];
	        this.wiki_url = source["wiki_url"];
	        this.description = source["description"];
	        this.spell_type = source["spell_type"];
	    }
	}
	export class SpellPayload {
	    name: string;
	    wiki_url: string;
	    description: string;
	    spell_type: string;
	    mana: number;
	    cast_time: string;
	    recovery_time: string;
	    recast_time: string;
	    spell_range: string;
	    aoe_range: string;
	    duration: string;
	    resist_type: string;
	    cast_on_you: string;
	    cast_on_other: string;
	    wears_off: string;
	    classes: SpellClassEntry[];
	
	    static createFrom(source: any = {}) {
	        return new SpellPayload(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.wiki_url = source["wiki_url"];
	        this.description = source["description"];
	        this.spell_type = source["spell_type"];
	        this.mana = source["mana"];
	        this.cast_time = source["cast_time"];
	        this.recovery_time = source["recovery_time"];
	        this.recast_time = source["recast_time"];
	        this.spell_range = source["spell_range"];
	        this.aoe_range = source["aoe_range"];
	        this.duration = source["duration"];
	        this.resist_type = source["resist_type"];
	        this.cast_on_you = source["cast_on_you"];
	        this.cast_on_other = source["cast_on_other"];
	        this.wears_off = source["wears_off"];
	        this.classes = this.convertValues(source["classes"], SpellClassEntry);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class StatusSnapshot {
	    eq_running: boolean;
	    configured: boolean;
	    log_file: string;
	    connected: boolean;
	    activity: string[];
	    version: string;
	
	    static createFrom(source: any = {}) {
	        return new StatusSnapshot(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.eq_running = source["eq_running"];
	        this.configured = source["configured"];
	        this.log_file = source["log_file"];
	        this.connected = source["connected"];
	        this.activity = source["activity"];
	        this.version = source["version"];
	    }
	}
	export class Tracker {
	    name: string;
	    role: string;
	    ago: string;
	
	    static createFrom(source: any = {}) {
	        return new Tracker(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.role = source["role"];
	        this.ago = source["ago"];
	    }
	}
	export class TimerEntry {
	    name: string;
	    status: string;
	    detail: string;
	    remaining: string;
	    trackers: Tracker[];
	    is_raid: boolean;
	    raid?: RaidCard;
	
	    static createFrom(source: any = {}) {
	        return new TimerEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.status = source["status"];
	        this.detail = source["detail"];
	        this.remaining = source["remaining"];
	        this.trackers = this.convertValues(source["trackers"], Tracker);
	        this.is_raid = source["is_raid"];
	        this.raid = this.convertValues(source["raid"], RaidCard);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class TimersData {
	    verified: boolean;
	    porter: string;
	    logistics: string;
	    idol: string;
	    mobs: TimerEntry[];
	    summary: string;
	    updated: string;
	    fetched_at: number;
	    batphones: BatphoneBanner[];
	    completed_raids: RaidCard[];
	
	    static createFrom(source: any = {}) {
	        return new TimersData(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.verified = source["verified"];
	        this.porter = source["porter"];
	        this.logistics = source["logistics"];
	        this.idol = source["idol"];
	        this.mobs = this.convertValues(source["mobs"], TimerEntry);
	        this.summary = source["summary"];
	        this.updated = source["updated"];
	        this.fetched_at = source["fetched_at"];
	        this.batphones = this.convertValues(source["batphones"], BatphoneBanner);
	        this.completed_raids = this.convertValues(source["completed_raids"], RaidCard);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class ZoneNick {
	    name: string;
	    nicks: string[];
	
	    static createFrom(source: any = {}) {
	        return new ZoneNick(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.nicks = source["nicks"];
	    }
	}
	export class wailsClientEntry {
	    id: number;
	    name: string;
	    toon: string;
	    guild: string;
	    last_zone: string;
	    version: string;
	    last_seen: number;
	    status: string;
	    muted: boolean;
	
	    static createFrom(source: any = {}) {
	        return new wailsClientEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.toon = source["toon"];
	        this.guild = source["guild"];
	        this.last_zone = source["last_zone"];
	        this.version = source["version"];
	        this.last_seen = source["last_seen"];
	        this.status = source["status"];
	        this.muted = source["muted"];
	    }
	}
	export class zoneChar {
	    name: string;
	    level: number;
	    class: string;
	    race: string;
	    guild: string;
	    afk: boolean;
	    lfg: boolean;
	
	    static createFrom(source: any = {}) {
	        return new zoneChar(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.level = source["level"];
	        this.class = source["class"];
	        this.race = source["race"];
	        this.guild = source["guild"];
	        this.afk = source["afk"];
	        this.lfg = source["lfg"];
	    }
	}
	export class wailsZoneData {
	    name: string;
	    last_seen: number;
	    characters: zoneChar[];
	
	    static createFrom(source: any = {}) {
	        return new wailsZoneData(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.last_seen = source["last_seen"];
	        this.characters = this.convertValues(source["characters"], zoneChar);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

