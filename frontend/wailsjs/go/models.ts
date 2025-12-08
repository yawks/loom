export namespace core {
	
	export class ProviderInfo {
	    id: string;
	    instanceId: string;
	    instanceName: string;
	    name: string;
	    description: string;
	    config: Record<string, any>;
	    isActive: boolean;
	    configSchema: Record<string, any>;
	
	    static createFrom(source: any = {}) {
	        return new ProviderInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.instanceId = source["instanceId"];
	        this.instanceName = source["instanceName"];
	        this.name = source["name"];
	        this.description = source["description"];
	        this.config = source["config"];
	        this.isActive = source["isActive"];
	        this.configSchema = source["configSchema"];
	    }
	}

}

export namespace gorm {
	
	export class DeletedAt {
	    Time: time.Time;
	    Valid: boolean;
	
	    static createFrom(source: any = {}) {
	        return new DeletedAt(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Time = this.convertValues(source["Time"], time.Time);
	        this.Valid = source["Valid"];
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

export namespace main {
	
	export class ClipboardFile {
	    filename: string;
	    base64: string;
	    mimeType: string;
	
	    static createFrom(source: any = {}) {
	        return new ClipboardFile(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.filename = source["filename"];
	        this.base64 = source["base64"];
	        this.mimeType = source["mimeType"];
	    }
	}

}

export namespace models {
	
	export class MessageReceipt {
	    id: number;
	    messageId: number;
	    userId: string;
	    receiptType: string;
	    timestamp: time.Time;
	    createdAt: time.Time;
	    updatedAt: time.Time;
	
	    static createFrom(source: any = {}) {
	        return new MessageReceipt(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.messageId = source["messageId"];
	        this.userId = source["userId"];
	        this.receiptType = source["receiptType"];
	        this.timestamp = this.convertValues(source["timestamp"], time.Time);
	        this.createdAt = this.convertValues(source["createdAt"], time.Time);
	        this.updatedAt = this.convertValues(source["updatedAt"], time.Time);
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
	export class Reaction {
	    id: number;
	    messageId: number;
	    userId: string;
	    emoji: string;
	    createdAt: time.Time;
	    updatedAt: time.Time;
	
	    static createFrom(source: any = {}) {
	        return new Reaction(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.messageId = source["messageId"];
	        this.userId = source["userId"];
	        this.emoji = source["emoji"];
	        this.createdAt = this.convertValues(source["createdAt"], time.Time);
	        this.updatedAt = this.convertValues(source["updatedAt"], time.Time);
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
	export class Message {
	    id: number;
	    conversationId: number;
	    protocolConvId: string;
	    protocolMsgId: string;
	    senderId: string;
	    senderName?: string;
	    senderAvatarUrl?: string;
	    body: string;
	    timestamp: time.Time;
	    isFromMe: boolean;
	    threadId?: string;
	    quotedMessageId?: string;
	    quotedSenderId?: string;
	    quotedSenderName?: string;
	    quotedBody?: string;
	    attachments: string;
	    reactions?: Reaction[];
	    receipts?: MessageReceipt[];
	    isStatusMessage: boolean;
	    isDeleted: boolean;
	    deletedBy?: string;
	    deletedReason?: string;
	    deletedTimestamp?: time.Time;
	    isEdited: boolean;
	    editedTimestamp?: time.Time;
	    callType?: string;
	    callDurationSecs?: number;
	    callParticipants?: string;
	    callOutcome?: string;
	    callIsVideo: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Message(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.conversationId = source["conversationId"];
	        this.protocolConvId = source["protocolConvId"];
	        this.protocolMsgId = source["protocolMsgId"];
	        this.senderId = source["senderId"];
	        this.senderName = source["senderName"];
	        this.senderAvatarUrl = source["senderAvatarUrl"];
	        this.body = source["body"];
	        this.timestamp = this.convertValues(source["timestamp"], time.Time);
	        this.isFromMe = source["isFromMe"];
	        this.threadId = source["threadId"];
	        this.quotedMessageId = source["quotedMessageId"];
	        this.quotedSenderId = source["quotedSenderId"];
	        this.quotedSenderName = source["quotedSenderName"];
	        this.quotedBody = source["quotedBody"];
	        this.attachments = source["attachments"];
	        this.reactions = this.convertValues(source["reactions"], Reaction);
	        this.receipts = this.convertValues(source["receipts"], MessageReceipt);
	        this.isStatusMessage = source["isStatusMessage"];
	        this.isDeleted = source["isDeleted"];
	        this.deletedBy = source["deletedBy"];
	        this.deletedReason = source["deletedReason"];
	        this.deletedTimestamp = this.convertValues(source["deletedTimestamp"], time.Time);
	        this.isEdited = source["isEdited"];
	        this.editedTimestamp = this.convertValues(source["editedTimestamp"], time.Time);
	        this.callType = source["callType"];
	        this.callDurationSecs = source["callDurationSecs"];
	        this.callParticipants = source["callParticipants"];
	        this.callOutcome = source["callOutcome"];
	        this.callIsVideo = source["callIsVideo"];
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
	export class GroupParticipant {
	    id: number;
	    conversationId: number;
	    userId: string;
	    isAdmin: boolean;
	    joinedAt: time.Time;
	    createdAt: time.Time;
	    updatedAt: time.Time;
	
	    static createFrom(source: any = {}) {
	        return new GroupParticipant(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.conversationId = source["conversationId"];
	        this.userId = source["userId"];
	        this.isAdmin = source["isAdmin"];
	        this.joinedAt = this.convertValues(source["joinedAt"], time.Time);
	        this.createdAt = this.convertValues(source["createdAt"], time.Time);
	        this.updatedAt = this.convertValues(source["updatedAt"], time.Time);
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
	export class Conversation {
	    id: number;
	    linkedAccountId: number;
	    protocolConvId: string;
	    isGroup: boolean;
	    groupName?: string;
	    isPinned: boolean;
	    isMuted: boolean;
	    groupParticipants?: GroupParticipant[];
	    messages: Message[];
	    createdAt: time.Time;
	    updatedAt: time.Time;
	
	    static createFrom(source: any = {}) {
	        return new Conversation(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.linkedAccountId = source["linkedAccountId"];
	        this.protocolConvId = source["protocolConvId"];
	        this.isGroup = source["isGroup"];
	        this.groupName = source["groupName"];
	        this.isPinned = source["isPinned"];
	        this.isMuted = source["isMuted"];
	        this.groupParticipants = this.convertValues(source["groupParticipants"], GroupParticipant);
	        this.messages = this.convertValues(source["messages"], Message);
	        this.createdAt = this.convertValues(source["createdAt"], time.Time);
	        this.updatedAt = this.convertValues(source["updatedAt"], time.Time);
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
	
	export class LinkedAccount {
	    id: number;
	    metaContactId: number;
	    protocol: string;
	    providerInstanceId: string;
	    userId: string;
	    username: string;
	    avatarUrl?: string;
	    status: string;
	    lastSeen?: time.Time;
	    extra?: string;
	    createdAt: time.Time;
	    updatedAt: time.Time;
	
	    static createFrom(source: any = {}) {
	        return new LinkedAccount(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.metaContactId = source["metaContactId"];
	        this.protocol = source["protocol"];
	        this.providerInstanceId = source["providerInstanceId"];
	        this.userId = source["userId"];
	        this.username = source["username"];
	        this.avatarUrl = source["avatarUrl"];
	        this.status = source["status"];
	        this.lastSeen = this.convertValues(source["lastSeen"], time.Time);
	        this.extra = source["extra"];
	        this.createdAt = this.convertValues(source["createdAt"], time.Time);
	        this.updatedAt = this.convertValues(source["updatedAt"], time.Time);
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
	
	
	export class MetaContact {
	    id: number;
	    displayName: string;
	    avatarUrl: string;
	    linkedAccounts: LinkedAccount[];
	    createdAt: time.Time;
	    updatedAt: time.Time;
	
	    static createFrom(source: any = {}) {
	        return new MetaContact(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.displayName = source["displayName"];
	        this.avatarUrl = source["avatarUrl"];
	        this.linkedAccounts = this.convertValues(source["linkedAccounts"], LinkedAccount);
	        this.createdAt = this.convertValues(source["createdAt"], time.Time);
	        this.updatedAt = this.convertValues(source["updatedAt"], time.Time);
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

export namespace time {
	
	export class Time {
	
	
	    static createFrom(source: any = {}) {
	        return new Time(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	
	    }
	}

}

