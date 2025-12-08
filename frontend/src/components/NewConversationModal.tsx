
import {
    Dialog,
    DialogContent,
    DialogFooter,
    DialogHeader,
    DialogTitle,
} from "@/components/ui/dialog";
import { useEffect, useMemo, useState } from "react";

import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Check, Search, X } from "lucide-react";
import { CreateGroup } from "../../wailsjs/go/main/App";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { ScrollArea } from "@/components/ui/scroll-area";
import { cn } from "@/lib/utils";
import { models } from "../../wailsjs/go/models";
import { useAppStore } from "@/lib/store";
import { useTranslation } from "react-i18next";

interface NewConversationModalProps {
    open: boolean;
    onOpenChange: (open: boolean) => void;
}

export function NewConversationModal({
    open,
    onOpenChange,
}: NewConversationModalProps) {
    const { t } = useTranslation();
    const metaContacts = useAppStore((state) => state.metaContacts);
    const setSelectedContact = useAppStore((state) => state.setSelectedContact);
    const [searchQuery, setSearchQuery] = useState("");
    const [selectedContacts, setSelectedContacts] = useState<models.MetaContact[]>(
        []
    );
    const [groupTitle, setGroupTitle] = useState("");
    const [isCreating, setIsCreating] = useState(false);

    // Reset state when modal opens/closes
    useEffect(() => {
        if (!open) {
            setSearchQuery("");
            setSelectedContacts([]);
            setGroupTitle("");
            setIsCreating(false);
        }
    }, [open]);

    // Filter contacts based on search query
    const filteredContacts = useMemo(() => {
        return metaContacts
            .filter((contact) => {
                // Filter out groups (contacts ending in @g.us)
                const isGroup = contact.linkedAccounts.some(acc => acc.userId.endsWith("@g.us"));
                if (isGroup) return false;

                const matchesSearch = contact.displayName
                    .toLowerCase()
                    .includes(searchQuery.toLowerCase());
                // Show all contacts, not just those with conversations
                // This is per user requirement: "tous ceux qui sont synchronisés"
                return matchesSearch;
            })
            .sort((a, b) => a.displayName.localeCompare(b.displayName));
    }, [metaContacts, searchQuery]);

    const handleToggleContact = (contact: models.MetaContact) => {
        setSelectedContacts((prev) => {
            const isSelected = prev.some((c) => c.id === contact.id);
            if (isSelected) {
                return prev.filter((c) => c.id !== contact.id);
            } else {
                return [...prev, contact];
            }
        });
    };

    const handleCreate = async () => {
        if (selectedContacts.length === 0) return;

        try {
            setIsCreating(true);

            if (selectedContacts.length === 1) {
                // Direct Message - just open it
                // No need to create anything on backend for DM
                setSelectedContact(selectedContacts[0]);
                onOpenChange(false);
            } else {
                // Group Conversation
                if (!groupTitle.trim()) {
                    // Optionally prompt for title? Or use default
                    // For now, let's require a title or default to something
                }

                const title = groupTitle.trim() || selectedContacts.slice(0, 3).map(c => c.displayName).join(", ");

                // Collect participant IDs
                // We need to pick specific linked accounts. 
                // For now, let's assume we pick the first available linked account for each contact.
                // Ideally we should filter by provider if we had a provider selector.
                // But req says: "ensuite une ligne avec un bouton permettant de choisir le provider (qd il y en a plusieurs)"
                // Since we didn't implement provider selector yet (and most users might have 1), 
                // let's just grab the first linked account ID
                const participantIDs = selectedContacts.map(c => c.linkedAccounts[0]?.userId).filter(Boolean) as string[];

                // Call backend
                await CreateGroup(title, participantIDs);

                // We really should wait for the new conversation to be synced/created and then select it.
                // But CreateGroup likely returns the conversation model (we implemented that).
                // Let's assume we wait for conversation list refresh or just close modal.
                // If CreateGroup returns the conversation, we could select it if we can map it to a MetaContact.
                // But MetaContact is constructed from DB.

                onOpenChange(false);
            }
        } catch (error) {
            console.error("Failed to create conversation:", error);
        } finally {
            setIsCreating(false);
        }
    };

    const isGroup = selectedContacts.length > 1;

    return (
        <Dialog open={open} onOpenChange={onOpenChange}>
            <DialogContent className="sm:max-w-[500px] h-[80vh] flex flex-col p-0 gap-0">
                <DialogHeader className="p-6 pb-2">
                    <DialogTitle>{t("new_conversation")}</DialogTitle>
                </DialogHeader>

                <div className="flex-1 flex flex-col overflow-hidden px-6">
                    {isGroup && (
                        <div className="pb-4 pt-2">
                            <Label htmlFor="group-title" className="mb-2 block">
                                {t("conversation_title")}
                            </Label>
                            <Input
                                id="group-title"
                                value={groupTitle}
                                onChange={(e) => setGroupTitle(e.target.value)}
                                placeholder={t("optional_group_title")}
                            />
                        </div>
                    )}

                    <div className="relative mb-4">
                        <Search className="absolute left-2.5 top-2.5 h-4 w-4 text-muted-foreground" />
                        <Input
                            placeholder={t("search_contacts")}
                            value={searchQuery}
                            onChange={(e) => setSearchQuery(e.target.value)}
                            className="pl-9"
                        />
                    </div>

                    {selectedContacts.length > 0 && (
                        <div className="flex flex-wrap gap-2 mb-4 max-h-[100px] overflow-y-auto">
                            {selectedContacts.map((contact) => (
                                <Badge
                                    key={contact.id}
                                    variant="secondary"
                                    className="pl-1 pr-2 py-1 flex items-center gap-1"
                                >
                                    <Avatar className="h-5 w-5">
                                        <AvatarImage src={contact.avatarUrl} />
                                        <AvatarFallback className="text-[10px]">
                                            {contact.displayName.substring(0, 2).toUpperCase()}
                                        </AvatarFallback>
                                    </Avatar>
                                    <span className="max-w-[100px] truncate">
                                        {contact.displayName}
                                    </span>
                                    <div
                                        className="cursor-pointer ml-1 hover:text-destructive"
                                        onClick={(e) => {
                                            e.stopPropagation();
                                            handleToggleContact(contact);
                                        }}
                                    >
                                        <X className="h-3 w-3" />
                                    </div>
                                </Badge>
                            ))}
                        </div>
                    )}

                    <div className="flex-1 overflow-hidden border rounded-md relative">
                        <ScrollArea className="h-full">
                            <div className="p-2 space-y-1">
                                {filteredContacts.length === 0 ? (
                                    <div className="text-center py-8 text-muted-foreground">
                                        {t("no_contacts_found")}
                                    </div>
                                ) : (
                                    filteredContacts.map((contact) => {
                                        const isSelected = selectedContacts.some(
                                            (c) => c.id === contact.id
                                        );
                                        return (
                                            <div
                                                key={contact.id}
                                                className={cn(
                                                    "flex items-center gap-3 p-2 rounded-lg cursor-pointer transition-colors",
                                                    isSelected
                                                        ? "bg-accent text-accent-foreground"
                                                        : "hover:bg-muted"
                                                )}
                                                onClick={() => handleToggleContact(contact)}
                                            >
                                                <div className="relative">
                                                    <Avatar>
                                                        <AvatarImage src={contact.avatarUrl} />
                                                        <AvatarFallback>
                                                            {contact.displayName.substring(0, 2).toUpperCase()}
                                                        </AvatarFallback>
                                                    </Avatar>
                                                    {isSelected && (
                                                        <div className="absolute -bottom-1 -right-1 bg-primary text-primary-foreground rounded-full p-0.5 border-2 border-background">
                                                            <Check className="h-3 w-3" />
                                                        </div>
                                                    )}
                                                </div>
                                                <div className="flex-1 min-w-0">
                                                    <div className="font-medium truncate">
                                                        {contact.displayName}
                                                    </div>
                                                    <div className="text-xs text-muted-foreground truncate">
                                                        {/* Show provider or phone info if available */}
                                                        {contact.linkedAccounts[0]?.protocol}
                                                    </div>
                                                </div>
                                            </div>
                                        );
                                    })
                                )}
                            </div>
                        </ScrollArea>
                    </div>
                </div>

                <DialogFooter className="p-6 pt-4 border-t mt-auto">
                    <Button variant="outline" onClick={() => onOpenChange(false)}>
                        {t("cancel")}
                    </Button>
                    <Button
                        onClick={handleCreate}
                        disabled={selectedContacts.length === 0 || isCreating}
                    >
                        {isCreating ? t("creating") : isGroup ? t("create_group") : t("open_chat")}
                    </Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
}
