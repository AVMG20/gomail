export namespace models {
	
	export class AIModel {
	    id: string;
	    name: string;
	    description: string;
	    contextLength: number;
	    promptPrice: number;
	    completionPrice: number;
	
	    static createFrom(source: any = {}) {
	        return new AIModel(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.description = source["description"];
	        this.contextLength = source["contextLength"];
	        this.promptPrice = source["promptPrice"];
	        this.completionPrice = source["completionPrice"];
	    }
	}
	export class Account {
	    id: string;
	    provider: string;
	    email: string;
	    displayName: string;
	    color: string;
	    // Go type: time
	    createdAt: any;
	    // Go type: time
	    lastSyncAt?: any;
	    syncError: string;
	    syncing: boolean;
	    unreadCount: number;
	
	    static createFrom(source: any = {}) {
	        return new Account(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.provider = source["provider"];
	        this.email = source["email"];
	        this.displayName = source["displayName"];
	        this.color = source["color"];
	        this.createdAt = this.convertValues(source["createdAt"], null);
	        this.lastSyncAt = this.convertValues(source["lastSyncAt"], null);
	        this.syncError = source["syncError"];
	        this.syncing = source["syncing"];
	        this.unreadCount = source["unreadCount"];
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
	export class Address {
	    name: string;
	    email: string;
	
	    static createFrom(source: any = {}) {
	        return new Address(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.email = source["email"];
	    }
	}
	export class Attachment {
	    id: string;
	    filename: string;
	    mimeType: string;
	    size: number;
	    inline: boolean;
	    contentId: string;
	
	    static createFrom(source: any = {}) {
	        return new Attachment(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.filename = source["filename"];
	        this.mimeType = source["mimeType"];
	        this.size = source["size"];
	        this.inline = source["inline"];
	        this.contentId = source["contentId"];
	    }
	}
	export class Category {
	    id: string;
	    name: string;
	    description: string;
	    color: string;
	
	    static createFrom(source: any = {}) {
	        return new Category(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.description = source["description"];
	        this.color = source["color"];
	    }
	}
	export class ClassificationResult {
	    messageId: string;
	    category: string;
	    confidence: number;
	    reason: string;
	
	    static createFrom(source: any = {}) {
	        return new ClassificationResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.messageId = source["messageId"];
	        this.category = source["category"];
	        this.confidence = source["confidence"];
	        this.reason = source["reason"];
	    }
	}
	export class OutgoingAttachment {
	    filename: string;
	    mimeType: string;
	    dataBase64: string;
	
	    static createFrom(source: any = {}) {
	        return new OutgoingAttachment(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.filename = source["filename"];
	        this.mimeType = source["mimeType"];
	        this.dataBase64 = source["dataBase64"];
	    }
	}
	export class ComposeRequest {
	    accountId: string;
	    to: Address[];
	    cc: Address[];
	    bcc: Address[];
	    subject: string;
	    bodyHtml: string;
	    bodyText: string;
	    replyToId: string;
	    mode: string;
	    attachments: OutgoingAttachment[];
	
	    static createFrom(source: any = {}) {
	        return new ComposeRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.accountId = source["accountId"];
	        this.to = this.convertValues(source["to"], Address);
	        this.cc = this.convertValues(source["cc"], Address);
	        this.bcc = this.convertValues(source["bcc"], Address);
	        this.subject = source["subject"];
	        this.bodyHtml = source["bodyHtml"];
	        this.bodyText = source["bodyText"];
	        this.replyToId = source["replyToId"];
	        this.mode = source["mode"];
	        this.attachments = this.convertValues(source["attachments"], OutgoingAttachment);
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
	export class Folder {
	    id: string;
	    accountId: string;
	    remoteId: string;
	    name: string;
	    kind: string;
	    unread: number;
	    total: number;
	
	    static createFrom(source: any = {}) {
	        return new Folder(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.accountId = source["accountId"];
	        this.remoteId = source["remoteId"];
	        this.name = source["name"];
	        this.kind = source["kind"];
	        this.unread = source["unread"];
	        this.total = source["total"];
	    }
	}
	export class ListQuery {
	    accountId: string;
	    folderId: string;
	    folderKind: string;
	    category: string;
	    search: string;
	    unreadOnly: boolean;
	    limit: number;
	    cursor: string;
	
	    static createFrom(source: any = {}) {
	        return new ListQuery(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.accountId = source["accountId"];
	        this.folderId = source["folderId"];
	        this.folderKind = source["folderKind"];
	        this.category = source["category"];
	        this.search = source["search"];
	        this.unreadOnly = source["unreadOnly"];
	        this.limit = source["limit"];
	        this.cursor = source["cursor"];
	    }
	}
	export class Message {
	    id: string;
	    accountId: string;
	    remoteId: string;
	    threadId: string;
	    folderIds: string[];
	    from: Address;
	    to: Address[];
	    subject: string;
	    snippet: string;
	    // Go type: time
	    date: any;
	    unread: boolean;
	    starred: boolean;
	    hasAttachments: boolean;
	    category: string;
	    cc: Address[];
	    bcc: Address[];
	    replyTo: Address[];
	    bodyHtml: string;
	    bodyText: string;
	    attachments: Attachment[];
	    messageId: string;
	    inReplyTo: string;
	    references: string;
	
	    static createFrom(source: any = {}) {
	        return new Message(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.accountId = source["accountId"];
	        this.remoteId = source["remoteId"];
	        this.threadId = source["threadId"];
	        this.folderIds = source["folderIds"];
	        this.from = this.convertValues(source["from"], Address);
	        this.to = this.convertValues(source["to"], Address);
	        this.subject = source["subject"];
	        this.snippet = source["snippet"];
	        this.date = this.convertValues(source["date"], null);
	        this.unread = source["unread"];
	        this.starred = source["starred"];
	        this.hasAttachments = source["hasAttachments"];
	        this.category = source["category"];
	        this.cc = this.convertValues(source["cc"], Address);
	        this.bcc = this.convertValues(source["bcc"], Address);
	        this.replyTo = this.convertValues(source["replyTo"], Address);
	        this.bodyHtml = source["bodyHtml"];
	        this.bodyText = source["bodyText"];
	        this.attachments = this.convertValues(source["attachments"], Attachment);
	        this.messageId = source["messageId"];
	        this.inReplyTo = source["inReplyTo"];
	        this.references = source["references"];
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
	export class MessageSummary {
	    id: string;
	    accountId: string;
	    remoteId: string;
	    threadId: string;
	    folderIds: string[];
	    from: Address;
	    to: Address[];
	    subject: string;
	    snippet: string;
	    // Go type: time
	    date: any;
	    unread: boolean;
	    starred: boolean;
	    hasAttachments: boolean;
	    category: string;
	
	    static createFrom(source: any = {}) {
	        return new MessageSummary(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.accountId = source["accountId"];
	        this.remoteId = source["remoteId"];
	        this.threadId = source["threadId"];
	        this.folderIds = source["folderIds"];
	        this.from = this.convertValues(source["from"], Address);
	        this.to = this.convertValues(source["to"], Address);
	        this.subject = source["subject"];
	        this.snippet = source["snippet"];
	        this.date = this.convertValues(source["date"], null);
	        this.unread = source["unread"];
	        this.starred = source["starred"];
	        this.hasAttachments = source["hasAttachments"];
	        this.category = source["category"];
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
	export class MessagePage {
	    messages: MessageSummary[];
	    nextCursor: string;
	    total: number;
	
	    static createFrom(source: any = {}) {
	        return new MessagePage(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.messages = this.convertValues(source["messages"], MessageSummary);
	        this.nextCursor = source["nextCursor"];
	        this.total = source["total"];
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
	    theme: string;
	    density: string;
	    syncIntervalMinutes: number;
	    showRemoteImages: boolean;
	    googleClientId: string;
	    googleClientSecret: string;
	    microsoftClientId: string;
	    microsoftTenant: string;
	    openRouterKeySet: boolean;
	    openRouterModel: string;
	    autoClassify: boolean;
	    categories: Category[];
	
	    static createFrom(source: any = {}) {
	        return new Settings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.theme = source["theme"];
	        this.density = source["density"];
	        this.syncIntervalMinutes = source["syncIntervalMinutes"];
	        this.showRemoteImages = source["showRemoteImages"];
	        this.googleClientId = source["googleClientId"];
	        this.googleClientSecret = source["googleClientSecret"];
	        this.microsoftClientId = source["microsoftClientId"];
	        this.microsoftTenant = source["microsoftTenant"];
	        this.openRouterKeySet = source["openRouterKeySet"];
	        this.openRouterModel = source["openRouterModel"];
	        this.autoClassify = source["autoClassify"];
	        this.categories = this.convertValues(source["categories"], Category);
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

