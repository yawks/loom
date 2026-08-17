export namespace core {
	
	export class Capabilities {
	    supportsThreads: boolean;
	    supportsReactions: boolean;
	    supportsCustomEmojis: boolean;
	    supportsTypingIndicator: boolean;
	    supportsGroupManagement: boolean;
	    supportsAddGroupMembers: boolean;
	    supportsRemoveGroupMembers: boolean;
	    supportsRenameGroup: boolean;
	    supportsGroupDescription: boolean;
	    supportsGroupPhoto: boolean;
	    supportsGroupAdminRoles: boolean;
	    supportsLeaveGroup: boolean;
	    supportsDeleteMessage: boolean;
	    supportsEditMessage: boolean;
	    supportsReadReceipts: boolean;
	    supportsPinConversation: boolean;
	    supportsPinMessage: boolean;
	    supportsListMessagePins: boolean;
	    supportsScheduledMessages: boolean;
	    supportsListScheduledMessages: boolean;
	    messagePinScope: string;
	    supportsMuteConversation: boolean;
	    supportsQRCodeAuth: boolean;
	    nativeEmojiReactions: boolean;
	    supportsContactDirectory: boolean;
	    supportsDirectConversation: boolean;
	    supportsPhoneNumberRecipient: boolean;
	    supportsGroupConversation: boolean;
	    supportsGroupTitle: boolean;
	    requiresGroupTitle: boolean;
	    groupConversationTypes: string;
	
	    static createFrom(source: any = {}) {
	        return new Capabilities(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.supportsThreads = source["supportsThreads"];
	        this.supportsReactions = source["supportsReactions"];
	        this.supportsCustomEmojis = source["supportsCustomEmojis"];
	        this.supportsTypingIndicator = source["supportsTypingIndicator"];
	        this.supportsGroupManagement = source["supportsGroupManagement"];
	        this.supportsAddGroupMembers = source["supportsAddGroupMembers"];
	        this.supportsRemoveGroupMembers = source["supportsRemoveGroupMembers"];
	        this.supportsRenameGroup = source["supportsRenameGroup"];
	        this.supportsGroupDescription = source["supportsGroupDescription"];
	        this.supportsGroupPhoto = source["supportsGroupPhoto"];
	        this.supportsGroupAdminRoles = source["supportsGroupAdminRoles"];
	        this.supportsLeaveGroup = source["supportsLeaveGroup"];
	        this.supportsDeleteMessage = source["supportsDeleteMessage"];
	        this.supportsEditMessage = source["supportsEditMessage"];
	        this.supportsReadReceipts = source["supportsReadReceipts"];
	        this.supportsPinConversation = source["supportsPinConversation"];
	        this.supportsPinMessage = source["supportsPinMessage"];
	        this.supportsListMessagePins = source["supportsListMessagePins"];
	        this.supportsScheduledMessages = source["supportsScheduledMessages"];
	        this.supportsListScheduledMessages = source["supportsListScheduledMessages"];
	        this.messagePinScope = source["messagePinScope"];
	        this.supportsMuteConversation = source["supportsMuteConversation"];
	        this.supportsQRCodeAuth = source["supportsQRCodeAuth"];
	        this.nativeEmojiReactions = source["nativeEmojiReactions"];
	        this.supportsContactDirectory = source["supportsContactDirectory"];
	        this.supportsDirectConversation = source["supportsDirectConversation"];
	        this.supportsPhoneNumberRecipient = source["supportsPhoneNumberRecipient"];
	        this.supportsGroupConversation = source["supportsGroupConversation"];
	        this.supportsGroupTitle = source["supportsGroupTitle"];
	        this.requiresGroupTitle = source["requiresGroupTitle"];
	        this.groupConversationTypes = source["groupConversationTypes"];
	    }
	}
	export class ProviderInfo {
	    id: string;
	    instanceId: string;
	    instanceName: string;
	    name: string;
	    description: string;
	    config: Record<string, any>;
	    isActive: boolean;
	    configSchema: Record<string, any>;
	    syncError: string;
	
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
	        this.syncError = source["syncError"];
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
	
	export class LinkPreview {
	    title: string;
	    description: string;
	    imageURL: string;
	    url: string;
	
	    static createFrom(source: any = {}) {
	        return new LinkPreview(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.title = source["title"];
	        this.description = source["description"];
	        this.imageURL = source["imageURL"];
	        this.url = source["url"];
	    }
	}

}

export namespace models {
	
	export class CommunicationCount {
	    total: number;
	    sent: number;
	    received: number;
	
	    static createFrom(source: any = {}) {
	        return new CommunicationCount(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.total = source["total"];
	        this.sent = source["sent"];
	        this.received = source["received"];
	    }
	}
	export class CommunicationSeriesPoint {
	    timestamp: time.Time;
	    total: number;
	    sent: number;
	    received: number;
	
	    static createFrom(source: any = {}) {
	        return new CommunicationSeriesPoint(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.timestamp = this.convertValues(source["timestamp"], time.Time);
	        this.total = source["total"];
	        this.sent = source["sent"];
	        this.received = source["received"];
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
	export class ContactCommunicationStats {
	    metaContactId: number;
	    displayName: string;
	    avatarUrl: string;
	    providerInstanceId: string;
	    providerId: string;
	    instanceName: string;
	    total: number;
	    sent: number;
	    received: number;
	    callCount: number;
	    callDurationSecs: number;
	    callsWithoutDuration: number;
	
	    static createFrom(source: any = {}) {
	        return new ContactCommunicationStats(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.metaContactId = source["metaContactId"];
	        this.displayName = source["displayName"];
	        this.avatarUrl = source["avatarUrl"];
	        this.providerInstanceId = source["providerInstanceId"];
	        this.providerId = source["providerId"];
	        this.instanceName = source["instanceName"];
	        this.total = source["total"];
	        this.sent = source["sent"];
	        this.received = source["received"];
	        this.callCount = source["callCount"];
	        this.callDurationSecs = source["callDurationSecs"];
	        this.callsWithoutDuration = source["callsWithoutDuration"];
	    }
	}
	export class InstanceCommunicationStats {
	    providerInstanceId: string;
	    providerId: string;
	    instanceName: string;
	    total: number;
	    sent: number;
	    received: number;
	    callCount: number;
	    callDurationSecs: number;
	    callsWithoutDuration: number;
	
	    static createFrom(source: any = {}) {
	        return new InstanceCommunicationStats(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.providerInstanceId = source["providerInstanceId"];
	        this.providerId = source["providerId"];
	        this.instanceName = source["instanceName"];
	        this.total = source["total"];
	        this.sent = source["sent"];
	        this.received = source["received"];
	        this.callCount = source["callCount"];
	        this.callDurationSecs = source["callDurationSecs"];
	        this.callsWithoutDuration = source["callsWithoutDuration"];
	    }
	}
	export class CommunicationStats {
	    from: time.Time;
	    to: time.Time;
	    summary: CommunicationCount;
	    previousSummary: CommunicationCount;
	    series: CommunicationSeriesPoint[];
	    instances: InstanceCommunicationStats[];
	    contacts: ContactCommunicationStats[];
	
	    static createFrom(source: any = {}) {
	        return new CommunicationStats(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.from = this.convertValues(source["from"], time.Time);
	        this.to = this.convertValues(source["to"], time.Time);
	        this.summary = this.convertValues(source["summary"], CommunicationCount);
	        this.previousSummary = this.convertValues(source["previousSummary"], CommunicationCount);
	        this.series = this.convertValues(source["series"], CommunicationSeriesPoint);
	        this.instances = this.convertValues(source["instances"], InstanceCommunicationStats);
	        this.contacts = this.convertValues(source["contacts"], ContactCommunicationStats);
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
	
	export class ContactExchangeStats {
	    isGroup: boolean;
	    totalMessages: number;
	    sentMessages: number;
	    receivedMessages: number;
	    activeDays: number;
	    attachmentMessages: number;
	    reactionsGiven: number;
	    reactionsReceived: number;
	    calls: number;
	    missedCalls: number;
	    totalCallDurationSecs: number;
	    firstExchange?: time.Time;
	    lastExchange?: time.Time;
	    medianContactResponseSecs?: number;
	    medianMyResponseSecs?: number;
	
	    static createFrom(source: any = {}) {
	        return new ContactExchangeStats(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.isGroup = source["isGroup"];
	        this.totalMessages = source["totalMessages"];
	        this.sentMessages = source["sentMessages"];
	        this.receivedMessages = source["receivedMessages"];
	        this.activeDays = source["activeDays"];
	        this.attachmentMessages = source["attachmentMessages"];
	        this.reactionsGiven = source["reactionsGiven"];
	        this.reactionsReceived = source["reactionsReceived"];
	        this.calls = source["calls"];
	        this.missedCalls = source["missedCalls"];
	        this.totalCallDurationSecs = source["totalCallDurationSecs"];
	        this.firstExchange = this.convertValues(source["firstExchange"], time.Time);
	        this.lastExchange = this.convertValues(source["lastExchange"], time.Time);
	        this.medianContactResponseSecs = source["medianContactResponseSecs"];
	        this.medianMyResponseSecs = source["medianMyResponseSecs"];
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
	export class ContactProfile {
	    userId: string;
	    displayName: string;
	    avatarUrl: string;
	    protocol: string;
	    providerInstanceId: string;
	    phoneNumbers: string[];
	    emails: string[];
	    address?: string;
	    company?: string;
	    jobTitle?: string;
	    department?: string;
	    timezone?: string;
	    presence?: string;
	    statusText?: string;
	    statusEmoji?: string;
	    lastSeen?: time.Time;
	    providerFields: Record<string, string>;
	
	    static createFrom(source: any = {}) {
	        return new ContactProfile(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.userId = source["userId"];
	        this.displayName = source["displayName"];
	        this.avatarUrl = source["avatarUrl"];
	        this.protocol = source["protocol"];
	        this.providerInstanceId = source["providerInstanceId"];
	        this.phoneNumbers = source["phoneNumbers"];
	        this.emails = source["emails"];
	        this.address = source["address"];
	        this.company = source["company"];
	        this.jobTitle = source["jobTitle"];
	        this.department = source["department"];
	        this.timezone = source["timezone"];
	        this.presence = source["presence"];
	        this.statusText = source["statusText"];
	        this.statusEmoji = source["statusEmoji"];
	        this.lastSeen = this.convertValues(source["lastSeen"], time.Time);
	        this.providerFields = source["providerFields"];
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
	    threadReplyCount: number;
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
	    isForwarded: boolean;
	    callType?: string;
	    callDurationSecs?: number;
	    callParticipants?: string;
	    callOutcome?: string;
	    callIsVideo: boolean;
	    callUrl?: string;
	    callLinkAction?: string;
	
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
	        this.threadReplyCount = source["threadReplyCount"];
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
	        this.isForwarded = source["isForwarded"];
	        this.callType = source["callType"];
	        this.callDurationSecs = source["callDurationSecs"];
	        this.callParticipants = source["callParticipants"];
	        this.callOutcome = source["callOutcome"];
	        this.callIsVideo = source["callIsVideo"];
	        this.callUrl = source["callUrl"];
	        this.callLinkAction = source["callLinkAction"];
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
	    isSelf: boolean;
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
	        this.isSelf = source["isSelf"];
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
	    conversationType?: string;
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
	        this.conversationType = source["conversationType"];
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
	    isGroup: boolean;
	    lastSeen?: time.Time;
	    extra?: string;
	    conversationId?: string;
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
	        this.isGroup = source["isGroup"];
	        this.lastSeen = this.convertValues(source["lastSeen"], time.Time);
	        this.extra = source["extra"];
	        this.conversationId = source["conversationId"];
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
	export class ConversationResolution {
	    matches: MetaContact[];
	    created?: MetaContact;
	
	    static createFrom(source: any = {}) {
	        return new ConversationResolution(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.matches = this.convertValues(source["matches"], MetaContact);
	        this.created = this.convertValues(source["created"], MetaContact);
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
	export class GroupDetails {
	    conversationId: string;
	    name: string;
	    description: string;
	    avatarUrl: string;
	    canSendMessages: boolean;
	
	    static createFrom(source: any = {}) {
	        return new GroupDetails(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.conversationId = source["conversationId"];
	        this.name = source["name"];
	        this.description = source["description"];
	        this.avatarUrl = source["avatarUrl"];
	        this.canSendMessages = source["canSendMessages"];
	    }
	}
	
	
	
	
	export class MessageContext {
	    targetMessageId: string;
	    messages: Message[];
	    hasMoreBefore: boolean;
	    hasMoreAfter: boolean;
	
	    static createFrom(source: any = {}) {
	        return new MessageContext(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.targetMessageId = source["targetMessageId"];
	        this.messages = this.convertValues(source["messages"], Message);
	        this.hasMoreBefore = source["hasMoreBefore"];
	        this.hasMoreAfter = source["hasMoreAfter"];
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
	export class MessagePin {
	    id: number;
	    providerInstanceId: string;
	    protocolConvId: string;
	    protocolMsgId: string;
	    senderId?: string;
	    messageIsFromMe: boolean;
	    scope: string;
	    resolution: string;
	    pinnedAt?: time.Time;
	    messageTimestamp?: time.Time;
	    providerPinId?: string;
	    message?: Message;
	    createdAt: time.Time;
	    updatedAt: time.Time;
	
	    static createFrom(source: any = {}) {
	        return new MessagePin(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.providerInstanceId = source["providerInstanceId"];
	        this.protocolConvId = source["protocolConvId"];
	        this.protocolMsgId = source["protocolMsgId"];
	        this.senderId = source["senderId"];
	        this.messageIsFromMe = source["messageIsFromMe"];
	        this.scope = source["scope"];
	        this.resolution = source["resolution"];
	        this.pinnedAt = this.convertValues(source["pinnedAt"], time.Time);
	        this.messageTimestamp = this.convertValues(source["messageTimestamp"], time.Time);
	        this.providerPinId = source["providerPinId"];
	        this.message = this.convertValues(source["message"], Message);
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
	
	export class MessageSearchResult {
	    message: Message;
	    metaContactId: number;
	    conversationName: string;
	    conversationAvatar: string;
	    protocol: string;
	    providerInstanceId: string;
	
	    static createFrom(source: any = {}) {
	        return new MessageSearchResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.message = this.convertValues(source["message"], Message);
	        this.metaContactId = source["metaContactId"];
	        this.conversationName = source["conversationName"];
	        this.conversationAvatar = source["conversationAvatar"];
	        this.protocol = source["protocol"];
	        this.providerInstanceId = source["providerInstanceId"];
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
	export class MessageSearchPage {
	    items: MessageSearchResult[];
	    hasMore: boolean;
	
	    static createFrom(source: any = {}) {
	        return new MessageSearchPage(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.items = this.convertValues(source["items"], MessageSearchResult);
	        this.hasMore = source["hasMore"];
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
	
	
	export class OpenConversationRequest {
	    providerInstanceId: string;
	    participantIds: string[];
	    conversationType: string;
	    title: string;
	
	    static createFrom(source: any = {}) {
	        return new OpenConversationRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.providerInstanceId = source["providerInstanceId"];
	        this.participantIds = source["participantIds"];
	        this.conversationType = source["conversationType"];
	        this.title = source["title"];
	    }
	}
	
	export class ScheduledMessage {
	    id: string;
	    providerInstanceId: string;
	    protocolConvId: string;
	    body: string;
	    scheduledAt: time.Time;
	    createdAt: time.Time;
	
	    static createFrom(source: any = {}) {
	        return new ScheduledMessage(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.providerInstanceId = source["providerInstanceId"];
	        this.protocolConvId = source["protocolConvId"];
	        this.body = source["body"];
	        this.scheduledAt = this.convertValues(source["scheduledAt"], time.Time);
	        this.createdAt = this.convertValues(source["createdAt"], time.Time);
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
	export class ThreadSummary {
	    parentMessageId: string;
	    replyCount: number;
	
	    static createFrom(source: any = {}) {
	        return new ThreadSummary(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.parentMessageId = source["parentMessageId"];
	        this.replyCount = source["replyCount"];
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

