import {
  ResizableHandle,
  ResizablePanel,
  ResizablePanelGroup,
} from "@/components/ui/resizable";
import { Suspense, useCallback, useEffect, useState } from "react";

import { AvatarModal } from "./AvatarModal";
import { Button } from "@/components/ui/button";
import { ContactList } from "./ContactList";
import { ContactListSkeleton } from "@/components/ContactListSkeleton";
import { ConversationDetailsView } from "./ConversationDetailsView";
import { EventsOn } from "../../wailsjs/runtime/runtime";
import { GetConfiguredProviders } from "../../wailsjs/go/main/App";
import { Header } from "./Header";
import { MessageList } from "./MessageList";
import { MessageListSkeleton } from "@/components/MessageListSkeleton";
import { ProviderFilterBar } from "./ProviderFilterBar";
import { ProvidersModal } from "./ProvidersModal";
import { SyncStatusFooter } from "./SyncStatusFooter";
import { ThreadView } from "./ThreadView";
import { useAppStore } from "@/lib/store";
import { useKeyboardShortcuts } from "@/hooks/useKeyboardShortcuts";
import { useMessageEvents } from "@/hooks/useMessageEvents";
import { useSystemTrayBadge } from "@/hooks/useSystemTrayBadge";
import { useTranslation } from "react-i18next";

export function ChatLayout() {
  const { t } = useTranslation();
  // Listen to real-time message events
  useMessageEvents();
  // Handle keyboard shortcuts
  useKeyboardShortcuts();
  // Update system tray badge with unread count
  useSystemTrayBadge();

  const selectedContact = useAppStore((state) => state.selectedContact);
  const showThreads = useAppStore((state) => state.showThreads);
  const selectedThreadId = useAppStore((state) => state.selectedThreadId);
  const showConversationDetails = useAppStore(
    (state) => state.showConversationDetails
  );
  const theme = useAppStore((state) => state.theme);

  // State for provider checking and onboarding
  const [showOnboarding, setShowOnboarding] = useState(false);
  const [showProvidersModal, setShowProvidersModal] = useState(false);

  // Function to check providers and update onboarding state
  const checkProviders = useCallback(async () => {
    try {
      const providers = await GetConfiguredProviders();
      const hasConfiguredProviders = providers && providers.length > 0;
      setShowOnboarding(!hasConfiguredProviders);
    } catch (error) {
      console.error("Failed to check providers:", error);
      // Assume providers exist on error to avoid blocking the UI
      setShowOnboarding(false);
    }
  }, []);

  // Check if providers are configured on mount
  useEffect(() => {
    // This is a valid use case: initializing state from async data on mount
    // The setState is called asynchronously inside checkProviders, not directly in the effect
    checkProviders();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Listen for contacts-refresh events (triggered when providers are added/removed)
  useEffect(() => {
    const unsubscribe = EventsOn("contacts-refresh", () => {
      // Recheck providers when contacts are refreshed (usually means provider was added/removed)
      checkProviders();
    });

    return () => {
      if (unsubscribe) {
        unsubscribe();
      }
    };
  }, [checkProviders]);

  // Refresh provider check when ProvidersModal closes
  const handleProvidersModalClose = async (open: boolean) => {
    setShowProvidersModal(open);
    if (!open) {
      // Recheck providers when modal closes
      await checkProviders();
    }
  };

  const handleConfigureProvider = () => {
    setShowProvidersModal(true);
  };

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
      {/* macOS title bar drag area */}
      <div className="h-[28px] bg-background" style={{ WebkitAppRegion: 'drag' } as React.CSSProperties} />
      <Header hasProviders={!showOnboarding} />
      {showOnboarding ? (
        // Onboarding screen when no providers configured
        <div className="flex-1 flex items-center justify-center bg-background">
          <div className="max-w-md mx-auto p-8 text-center space-y-6">
            <div className="flex items-center justify-center mb-6">
              <div className="rounded-full bg-primary/10 p-2">
                <img src="/appicon.png" alt="Loom" className="h-16 w-16" />
              </div>
            </div>
            <div className="space-y-3">
              <h1 className="text-3xl font-bold tracking-tight">
                {t("onboarding_welcome_title")}
              </h1>
              <p className="text-lg text-muted-foreground leading-relaxed">
                {t("onboarding_welcome_description")}
              </p>
            </div>
            <div className="pt-4">
              <Button
                onClick={handleConfigureProvider}
                size="lg"
                className="w-full sm:w-auto"
              >
                {t("onboarding_configure_button")}
              </Button>
            </div>
          </div>
        </div>
      ) : (
        // Normal chat layout when providers are configured
        <>
          <ResizablePanelGroup direction="horizontal" className="flex-1">
            <ProviderFilterBar />
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
        </>
      )}
      <AvatarModal />
      <ProvidersModal
        open={showProvidersModal}
        onOpenChange={handleProvidersModalClose}
      />
    </div>
  );
}
