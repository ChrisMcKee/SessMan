export namespace domain {
	
	export class Integration {
	    id: string;
	    name: string;
	    startUrl: string;
	    ssoRegion: string;
	    status: string;
	
	    static createFrom(source: any = {}) {
	        return new Integration(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.startUrl = source["startUrl"];
	        this.ssoRegion = source["ssoRegion"];
	        this.status = source["status"];
	    }
	}
	export class Session {
	    id: string;
	    integrationId: string;
	    accountId: string;
	    accountName: string;
	    roleName: string;
	    profileName: string;
	    region: string;
	    state: string;
	    pinned?: boolean;
	    expiresAt?: string;
	    lastError?: string;
	
	    static createFrom(source: any = {}) {
	        return new Session(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.integrationId = source["integrationId"];
	        this.accountId = source["accountId"];
	        this.accountName = source["accountName"];
	        this.roleName = source["roleName"];
	        this.profileName = source["profileName"];
	        this.region = source["region"];
	        this.state = source["state"];
	        this.pinned = source["pinned"];
	        this.expiresAt = source["expiresAt"];
	        this.lastError = source["lastError"];
	    }
	}
	export class Settings {
	    defaultRegion: string;
	    rotationIntervalMin: number;
	    syncProfileRegion: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Settings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.defaultRegion = source["defaultRegion"];
	        this.rotationIntervalMin = source["rotationIntervalMin"];
	        this.syncProfileRegion = source["syncProfileRegion"];
	    }
	}

}

export namespace eksmgr {
	
	export class Cluster {
	    name: string;
	    status: string;
	    version: string;
	    arn: string;
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new Cluster(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.status = source["status"];
	        this.version = source["version"];
	        this.arn = source["arn"];
	        this.error = source["error"];
	    }
	}
	export class SetupStatus {
	    awsCli: boolean;
	    ready: boolean;
	    awsCliUrl: string;
	
	    static createFrom(source: any = {}) {
	        return new SetupStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.awsCli = source["awsCli"];
	        this.ready = source["ready"];
	        this.awsCliUrl = source["awsCliUrl"];
	    }
	}

}

export namespace sessionmgr {
	
	export class AddIntegrationInput {
	    name: string;
	    startUrl: string;
	    ssoRegion: string;
	
	    static createFrom(source: any = {}) {
	        return new AddIntegrationInput(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.startUrl = source["startUrl"];
	        this.ssoRegion = source["ssoRegion"];
	    }
	}

}

export namespace ssmmgr {
	
	export class Instance {
	    instanceId: string;
	    name: string;
	    pingStatus: string;
	    platformName: string;
	    platformType: string;
	    ipAddress: string;
	
	    static createFrom(source: any = {}) {
	        return new Instance(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.instanceId = source["instanceId"];
	        this.name = source["name"];
	        this.pingStatus = source["pingStatus"];
	        this.platformName = source["platformName"];
	        this.platformType = source["platformType"];
	        this.ipAddress = source["ipAddress"];
	    }
	}
	export class SetupStatus {
	    awsCli: boolean;
	    plugin: boolean;
	    ready: boolean;
	    awsCliUrl: string;
	    pluginUrl: string;
	
	    static createFrom(source: any = {}) {
	        return new SetupStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.awsCli = source["awsCli"];
	        this.plugin = source["plugin"];
	        this.ready = source["ready"];
	        this.awsCliUrl = source["awsCliUrl"];
	        this.pluginUrl = source["pluginUrl"];
	    }
	}

}

