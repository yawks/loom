import {
  ResizableHandle,
  ResizablePanel,
  ResizablePanelGroup,
} from "@/components/ui/resizable";

import { AvatarModal } from "./AvatarModal";
import { ContactList } from "./ContactList";
import { ContactListSkeleton } from "@/components/ContactListSkeleton";
import { ConversationDetailsView } from "./ConversationDetailsView";
import { Header } from "./Header";
import { MessageList } from "./MessageList";
import { MessageListSkeleton } from "@/components/MessageListSkeleton";
import { Suspense } from "react";
import { SyncStatusFooter } from "./SyncStatusFooter";
import { ThreadView } from "./ThreadView";
import { useAppStore } from "@/lib/store";
import { useMessageEvents } from "@/hooks/useMessageEvents";
import { useTranslation } from "react-i18next";

export function ChatLayout() {
  const { t } = useTranslation();
  // Listen to real-time message events
  useMessageEvents();
  
  const selectedContact = useAppStore((state) => state.selectedContact);
  const showThreads = useAppStore((state) => state.showThreads);
  const selectedThreadId = useAppStore((state) => state.selectedThreadId);
  const showConversationDetails = useAppStore(
    (state) => state.showConversationDetails
  );
  const theme = useAppStore((state) => state.theme);
  
  // Show threads panel only if it's toggled on and a thread is selected
  const shouldShowThreadsPanel = showThreads && selectedThreadId !== null;
  // Show conversation details panel if toggled on
  const shouldShowDetailsPanel = showConversationDetails && selectedContact !== null;

  // Calculate panel sizes based on which sidebars are visible
  const getMessagesPanelSize = () => {
    if (shouldShowThreadsPanel && shouldShowDetailsPanel) return 40;
    if (shouldShowThreadsPanel || shouldShowDetailsPanel) return 50;
    return 75;
  };

  return (
    <div className="flex flex-col h-screen">
      <Header />
      <ResizablePanelGroup direction="horizontal" className="flex-1">
        <ResizablePanel id="contacts-panel" defaultSize={25} minSize={15}>
          <Suspense fallback={<ContactListSkeleton />}>
            <ContactList />
          </Suspense>
        </ResizablePanel>
        <ResizableHandle withHandle />
        <ResizablePanel
          id="messages-panel"
          defaultSize={getMessagesPanelSize()}
          minSize={30}
        >
          <Suspense fallback={<MessageListSkeleton />}>
            {selectedContact ? (
              <MessageList selectedConversation={selectedContact} />
            ) : (
              <div className="h-full flex flex-col items-center justify-center text-muted-foreground">
                <img 
                  src="https://api.iconify.design/marketeq:conversation.svg" 
                  className="h-16 w-16 mb-4 opacity-50" 
                  style={{
                    filter: theme === "dark" 
                      ? "grayscale(1) invert(1) brightness(1.5)" 
                      : "none"
                  }}
                  alt="Conversation icon"
                />
                <p className="text-xl font-medium">{t("select_a_conversation")}</p>
              </div>
            )}
          </Suspense>
        </ResizablePanel>
        {shouldShowDetailsPanel && (
          <>
            <ResizableHandle withHandle />
            <ResizablePanel id="details-panel" defaultSize={25} minSize={15}>
              <ConversationDetailsView
                selectedConversation={selectedContact!}
              />
            </ResizablePanel>
          </>
        )}
        {shouldShowThreadsPanel && (
          <>
            <ResizableHandle withHandle />
            <ResizablePanel id="threads-panel" defaultSize={25} minSize={15}>
              <ThreadView />
            </ResizablePanel>
          </>
        )}
      </ResizablePanelGroup>
      <SyncStatusFooter />
      <AvatarModal />
    </div>
  );
}
