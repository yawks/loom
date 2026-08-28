import { GetContactAliases, GetGroupDetails, GetMessagesForConversation, LeaveGroup, UpdateGroupDescription, UpdateGroupName, UpdateGroupPhoto } from "../../wailsjs/go/main/App";
import { Suspense, useEffect, useMemo, useRef, useState } from "react";
import { useQuery, useQueryClient, useSuspenseQuery } from "@tanstack/react-query";
import { EventsOn } from "../../wailsjs/runtime/runtime";

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import { Input } from "@/components/ui/input";
import { FileUploadModal } from "./FileUploadModal";
import { ParticipantListSkeleton } from "./ParticipantListSkeleton";
import { ParticipantsList } from "./ParticipantsList";
import { Camera, Check, LogOut, Pencil, X } from "lucide-react";
import { cn } from "@/lib/utils";
import type { models } from "../../wailsjs/go/models";
import { useAppStore } from "@/lib/store";
import { useFileUpload } from "@/hooks/useFileUpload";
import { useTranslation } from "react-i18next";
import { ToastContainer, useToast } from "@/components/ui/toast";
import { MessageWatchRules } from "./MessageWatchRules";

const fetchMessages = async (conversationID: string): Promise<models.Message[]> => {
  const result = await GetMessagesForConversation(conversationID);
  // Ensure we always return an array
  return Array.isArray(result) ? result : [];
};

interface ConversationDetailsViewProps {
  selectedConversation: models.MetaContact;
}

export function ConversationDetailsView({
  selectedConversation,
}: ConversationDetailsViewProps) {
  const { t } = useTranslation();
  const setShowConversationDetails = useAppStore(
    (state) => state.setShowConversationDetails
  );
  const setSelectedContactProfile = useAppStore((state) => state.setSelectedContactProfile);
  const renameGroupConversation = useAppStore((state) => state.renameGroupConversation);
  const capabilities = useAppStore((state) => state.capabilities);
  const selectedProviderFilter = useAppStore((state) => state.selectedProviderFilter);
  const [participantsCount, setParticipantsCount] = useState<number | null>(null);
  const [idCopied, setIdCopied] = useState(false);
  const [leaveConfirmOpen, setLeaveConfirmOpen] = useState(false);
  const [isLeaving, setIsLeaving] = useState(false);
  const [leftConversationId, setLeftConversationId] = useState<string | null>(null);
  const [editingName, setEditingName] = useState(false);
  const [editingDescription, setEditingDescription] = useState(false);
  const [groupName, setGroupName] = useState("");
  const [groupDescription, setGroupDescription] = useState("");
  const [savingGroupDetails, setSavingGroupDetails] = useState(false);
  const photoInputRef = useRef<HTMLInputElement>(null);
  const queryClient = useQueryClient();
  const { toasts, showToast, closeToast } = useToast();

  const selectedAccount =
    selectedConversation.linkedAccounts.find(
      (account) => !selectedProviderFilter || account.providerInstanceId === selectedProviderFilter
    ) ?? selectedConversation.linkedAccounts[0];
  const conversationId =
    selectedAccount?.conversationId ||
    (selectedAccount?.providerInstanceId && selectedAccount?.userId
      ? `${selectedAccount.providerInstanceId}::${selectedAccount.userId}`
      : selectedAccount?.userId) ||
    "";
  const groupCapabilities = selectedAccount?.providerInstanceId
    ? capabilities[selectedAccount.providerInstanceId]
    : undefined;
  const canAddMembers = Boolean(selectedAccount?.isGroup && groupCapabilities?.supportsAddGroupMembers);
  const canRemoveMembers = Boolean(selectedAccount?.isGroup && groupCapabilities?.supportsRemoveGroupMembers);
  const canManageAdmins = Boolean(selectedAccount?.isGroup && groupCapabilities?.supportsGroupAdminRoles);

  const { data: groupDetails } = useQuery<models.GroupDetails>({
    queryKey: ["group-details", conversationId],
    queryFn: () => GetGroupDetails(conversationId),
    enabled: Boolean(selectedAccount?.isGroup && conversationId),
    refetchInterval: 15000,
  });
  const canLeaveGroup = Boolean(
    leftConversationId !== conversationId &&
    groupDetails?.isMember !== false &&
    selectedAccount?.isGroup &&
    selectedAccount.providerInstanceId &&
    capabilities[selectedAccount.providerInstanceId]?.supportsLeaveGroup
  );

  useEffect(() => {
    setGroupName(groupDetails?.name ?? selectedConversation.displayName ?? "");
    setGroupDescription(groupDetails?.description ?? "");
  }, [groupDetails, selectedConversation.displayName]);

  useEffect(() => EventsOn("group-change", () => {
    void queryClient.invalidateQueries({ queryKey: ["group-details", conversationId] });
    void queryClient.invalidateQueries({ queryKey: ["participantsData", conversationId] });
  }), [conversationId, queryClient]);

  const saveName = async () => {
    setSavingGroupDetails(true);
    try {
      const updatedName = groupName.trim();
      await UpdateGroupName(conversationId, updatedName);
      renameGroupConversation(conversationId, updatedName);
      queryClient.setQueryData<models.GroupDetails>(["group-details", conversationId], (current) =>
        current ? { ...current, name: updatedName } as models.GroupDetails : current
      );
      setEditingName(false);
      await queryClient.invalidateQueries({ queryKey: ["group-details", conversationId] });
      showToast(t("group_name_updated"), "success");
    } catch (error) {
      showToast(`${t("group_details_update_error")}: ${String(error)}`, "error");
    } finally { setSavingGroupDetails(false); }
  };

  const saveDescription = async () => {
    setSavingGroupDetails(true);
    try {
      await UpdateGroupDescription(conversationId, groupDescription);
      setEditingDescription(false);
      await queryClient.invalidateQueries({ queryKey: ["group-details", conversationId] });
      showToast(t("group_description_updated"), "success");
    } catch (error) {
      showToast(`${t("group_details_update_error")}: ${String(error)}`, "error");
    } finally { setSavingGroupDetails(false); }
  };

  const updatePhoto = async (file?: File) => {
    if (!file) return;
    const reader = new FileReader();
    reader.onload = async () => {
      setSavingGroupDetails(true);
      try {
        await UpdateGroupPhoto(conversationId, String(reader.result));
        await queryClient.invalidateQueries({ queryKey: ["group-details", conversationId] });
        showToast(t("group_photo_updated"), "success");
      } catch (error) {
        showToast(`${t("group_details_update_error")}: ${String(error)}`, "error");
      } finally { setSavingGroupDetails(false); }
    };
    reader.readAsDataURL(file);
  };

  const {
    isDragging,
    isFileUploadModalOpen,
    setIsFileUploadModalOpen,
    pendingFiles,
    pendingFilePaths,
    uploadState,
    handleDragEnter,
    handleDragLeave,
    handleDragOver,
    handleDrop,
    handleFileUpload,
  } = useFileUpload(conversationId);

  // Use a different query key to avoid conflicts with MessageList's useInfiniteQuery
  const { data: messagesData } = useSuspenseQuery<models.Message[], Error>({
    queryKey: ["messages-details", conversationId],
    queryFn: () => fetchMessages(conversationId),
  });

  // Ensure messages is always an array
  const messages = useMemo(() => {
    if (!messagesData || !Array.isArray(messagesData)) {
      return [];
    }
    return messagesData;
  }, [messagesData]);

  const { data: aliases = {} } = useQuery<Record<string, string>, Error>({
    queryKey: ["contactAliases"],
    queryFn: async () => {
      const aliasMap = await GetContactAliases();
      return aliasMap || {};
    },
  });

  const handleClose = () => {
    setShowConversationDetails(false);
  };

  const handleAvatarClick = (avatarUrl: string | undefined, displayName: string, userId: string, status: string) => {
    setSelectedContactProfile({ conversationId, avatarUrl, displayName, userId, status });
  };

  const handleLeaveGroup = async () => {
    setIsLeaving(true);
    try {
      await LeaveGroup(conversationId);
      queryClient.setQueryData<models.GroupDetails>(["group-details", conversationId], (current) =>
        current ? { ...current, isMember: false, canSendMessages: false } as models.GroupDetails : current
      );
      await queryClient.invalidateQueries({ queryKey: ["group-details", conversationId] });
      setLeaveConfirmOpen(false);
      setLeftConversationId(conversationId);
      showToast(t("leave_group_success"), "success");
    } catch (error) {
      console.error("Failed to leave group:", error);
      showToast(t("leave_group_error"), "error");
    } finally {
      setIsLeaving(false);
    }
  };

  return (
    <div
      className={cn(
        "flex flex-col h-full transition-colors",
        isDragging && "bg-muted/50"
      )}
      onDragEnter={handleDragEnter}
      onDragLeave={handleDragLeave}
      onDragOver={handleDragOver}
      onDrop={handleDrop}
    >
      <div className="p-4 border-b flex justify-between items-center shrink-0">
        <h3 className="text-md font-semibold">{t("conversation_details")}</h3>
        <Button variant="ghost" size="icon" onClick={handleClose}>
          <X className="h-4 w-4" />
        </Button>
      </div>
      <div className="flex-1 overflow-y-auto p-4 min-h-0 scroll-area">
        <div className="space-y-6">
          {selectedAccount?.isGroup && (
            <div className="space-y-4">
              <div className="flex justify-center">
                <div className="relative">
                  <Avatar className="h-24 w-24">
                    <AvatarImage src={groupDetails?.avatarUrl || selectedAccount.avatarUrl} />
                    <AvatarFallback>{(groupDetails?.name || selectedConversation.displayName).slice(0, 2).toUpperCase()}</AvatarFallback>
                  </Avatar>
                  {groupCapabilities?.supportsGroupPhoto && (
                    <Button size="icon" className="absolute -bottom-1 -right-1 rounded-full h-8 w-8" onClick={() => photoInputRef.current?.click()} disabled={savingGroupDetails}>
                      <Camera className="h-4 w-4" />
                    </Button>
                  )}
                  <input ref={photoInputRef} type="file" accept="image/*" className="hidden" onChange={(event) => void updatePhoto(event.target.files?.[0])} />
                </div>
              </div>
              <div className="space-y-2">
                <label className="text-xs font-medium text-muted-foreground">{t("group_name")}</label>
                <div className="flex gap-2">
                  <Input value={groupName} onChange={(event) => setGroupName(event.target.value)} disabled={!editingName} spellCheck={false} />
                  {groupCapabilities?.supportsRenameGroup && (
                    <Button size="icon" variant="outline" disabled={savingGroupDetails} onClick={() => editingName ? void saveName() : setEditingName(true)}>
                      {editingName ? <Check className="h-4 w-4" /> : <Pencil className="h-4 w-4" />}
                    </Button>
                  )}
                </div>
              </div>
              {groupCapabilities?.supportsGroupDescription && (
                <div className="space-y-2">
                  <label className="text-xs font-medium text-muted-foreground">{t("group_description")}</label>
                  <div className="flex gap-2 items-start">
                    <textarea className="flex min-h-20 w-full rounded-md border border-input bg-background px-3 py-2 text-sm disabled:opacity-70" value={groupDescription} onChange={(event) => setGroupDescription(event.target.value)} disabled={!editingDescription} placeholder={t("no_group_description")} spellCheck={false} />
                    <Button size="icon" variant="outline" disabled={savingGroupDetails} onClick={() => editingDescription ? void saveDescription() : setEditingDescription(true)}>
                      {editingDescription ? <Check className="h-4 w-4" /> : <Pencil className="h-4 w-4" />}
                    </Button>
                  </div>
                </div>
              )}
            </div>
          )}
          {/* Participants */}
          <div>
            <div className="flex items-center justify-between mb-3">
              <h4 className="text-sm font-semibold text-muted-foreground">
                {t("participants")}{participantsCount !== null ? ` (${participantsCount})` : ""}
              </h4>
            </div>
            <Suspense fallback={<ParticipantListSkeleton />}>
              <ParticipantsList
                conversationId={conversationId}
                messages={messages}
                selectedConversation={selectedConversation}
                aliases={aliases}
                onAvatarClick={handleAvatarClick}
                onParticipantsCountChange={setParticipantsCount}
                providerInstanceId={selectedAccount?.providerInstanceId ?? ""}
                isGroup={selectedAccount?.isGroup ?? false}
                canAddMembers={canAddMembers}
                canRemoveMembers={canRemoveMembers}
                canManageAdmins={canManageAdmins}
              />
            </Suspense>
          </div>

          {conversationId && <MessageWatchRules conversationId={conversationId} />}

          {/* Debug: conversation ID */}
          {conversationId && (
            <div className="conversation-details__debug-id pt-2 border-t">
              <button
                className="w-full"
                title="Click to copy"
                onClick={() => {
                  navigator.clipboard.writeText(conversationId);
                  setIdCopied(true);
                  setTimeout(() => setIdCopied(false), 1500);
                }}
              >
                <p className="text-[10px] text-muted-foreground/50 font-mono break-all leading-relaxed hover:text-muted-foreground transition-colors text-center">
                  {idCopied ? "✓ copied" : conversationId}
                </p>
              </button>
            </div>
          )}

          {canLeaveGroup && (
            <div className="pt-2 border-t">
              <Button
                variant="destructive"
                className="w-full"
                onClick={() => setLeaveConfirmOpen(true)}
              >
                <LogOut className="h-4 w-4 mr-2" />
                {t("leave_group")}
              </Button>
            </div>
          )}
        </div>
      </div>
      <FileUploadModal
        open={isFileUploadModalOpen}
        onOpenChange={setIsFileUploadModalOpen}
        files={pendingFiles}
        filePaths={pendingFilePaths.length > 0 ? pendingFilePaths : undefined}
        uploadState={uploadState}
        onConfirm={handleFileUpload}
      />
      <AlertDialog open={leaveConfirmOpen} onOpenChange={setLeaveConfirmOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t("leave_group_title")}</AlertDialogTitle>
            <AlertDialogDescription>{t("leave_group_description")}</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={isLeaving}>{t("cancel")}</AlertDialogCancel>
            <AlertDialogAction
              disabled={isLeaving}
              onClick={(event) => {
                event.preventDefault();
                void handleLeaveGroup();
              }}
              className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
            >
              {isLeaving ? t("leaving_group") : t("leave_group")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
      <ToastContainer toasts={toasts} onClose={closeToast} />
    </div>
  );
}
