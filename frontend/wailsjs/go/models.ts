export namespace main {
	
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
	export class Settings {
	    guild_chat: boolean;
	    guild_motd: boolean;
	    broadcasts: boolean;
	    server_messages: boolean;
	    quake_messages: boolean;
	    engage_messages: boolean;
	    who_output: boolean;
	    character_locations: boolean;
	    share_map_position: boolean;
	    exclude_bots: boolean;
	    exclude_filtered: boolean;
	    startup_configured: boolean;
	    eq_directory: string;
	    admin_mode: boolean;
	    slain_messages: boolean;
	
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
	        this.share_map_position = source["share_map_position"];
	        this.exclude_bots = source["exclude_bots"];
	        this.exclude_filtered = source["exclude_filtered"];
	        this.startup_configured = source["startup_configured"];
	        this.eq_directory = source["eq_directory"];
	        this.admin_mode = source["admin_mode"];
	        this.slain_messages = source["slain_messages"];
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
	export class TimerEntry {
	    name: string;
	    status: string;
	    detail: string;
	    trackers: string;
	
	    static createFrom(source: any = {}) {
	        return new TimerEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.status = source["status"];
	        this.detail = source["detail"];
	        this.trackers = source["trackers"];
	    }
	}
	export class TimersData {
	    verified: boolean;
	    porter: string;
	    mobs: TimerEntry[];
	    summary: string;
	    updated: string;
	    fetched_at: number;
	
	    static createFrom(source: any = {}) {
	        return new TimersData(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.verified = source["verified"];
	        this.porter = source["porter"];
	        this.mobs = this.convertValues(source["mobs"], TimerEntry);
	        this.summary = source["summary"];
	        this.updated = source["updated"];
	        this.fetched_at = source["fetched_at"];
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
	    name: string;
	    version: string;
	    last_seen: number;
	    status: string;
	
	    static createFrom(source: any = {}) {
	        return new wailsClientEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.version = source["version"];
	        this.last_seen = source["last_seen"];
	        this.status = source["status"];
	    }
	}
	export class zoneChar {
	    name: string;
	    level: number;
	    class: string;
	    race: string;
	    guild: string;
	
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

