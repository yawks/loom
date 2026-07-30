import { GetContactAliases, GetMessagesForConversation } from "../../wailsjs/go/main/App";
import { Suspense, useMemo, useState } from "react";
import { useQuery, useSuspenseQuery } from "@tanstack/react-query";

import { Button } from "@/components/ui/button";
import { FileUploadModal } from "./FileUploadModal";
import { ParticipantListSkeleton } from "./ParticipantListSkeleton";
import { ParticipantsList } from "./ParticipantsList";
import { ContactProfileDialog } from "./ContactProfileDialog";
import { X } from "lucide-react";
import { cn } from "@/lib/utils";
import type { models } from "../../wailsjs/go/models";
import { useAppStore } from "@/lib/store";
import { useFileUpload } from "@/hooks/useFileUpload";
import { useTranslation } from "react-i18next";

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
  const [selectedParticipant, setSelectedParticipant] = useState<{
    userId: string;
    displayName: string;
    avatarUrl?: string;
  } | null>(null);
  const [participantsCount, setParticipantsCount] = useState<number | null>(null);
  const [idCopied, setIdCopied] = useState(false);

  const conversationId =
    selectedConversation.linkedAccounts[0]?.conversationId ??
    selectedConversation.linkedAccounts[0]?.userId ??
    "";

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

  const handleAvatarClick = (avatarUrl: string | undefined, displayName: string, userId: string) => {
    setSelectedParticipant({ avatarUrl, displayName, userId });
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
          {/* Participants */}
          <div>
            <h4 className="text-sm font-semibold text-muted-foreground mb-3">
              {t("participants")}{participantsCount !== null ? ` (${participantsCount})` : ""}
            </h4>
            <Suspense fallback={<ParticipantListSkeleton />}>
              <ParticipantsList
                conversationId={conversationId}
                messages={messages}
                selectedConversation={selectedConversation}
                aliases={aliases}
                onAvatarClick={handleAvatarClick}
                onParticipantsCountChange={setParticipantsCount}
              />
            </Suspense>
          </div>

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
      <ContactProfileDialog
        conversationId={conversationId}
        participant={selectedParticipant}
        onClose={() => setSelectedParticipant(null)}
      />
    </div>
  );
}
