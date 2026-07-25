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
import { ConversationDetailsViewSkeleton } from "./ConversationDetailsViewSkeleton";
import { EventsOn, WindowToggleMaximise } from "../../wailsjs/runtime/runtime";
import { GetConfiguredProviders } from "../../wailsjs/go/main/App";
import { MessageList } from "./MessageList";
import { MessageListSkeleton } from "@/components/MessageListSkeleton";
import { ProviderFilterBar } from "./ProviderFilterBar";
import { ProvidersModal } from "./ProvidersModal";
import { SearchModal } from "./SearchModal";
import { SettingsModal } from "./SettingsModal";
import { SyncStatusFooter } from "./SyncStatusFooter";
import { ThreadView } from "./ThreadView";
import { useAppStore } from "@/lib/store";
import { useKeyboardShortcuts } from "@/hooks/useKeyboardShortcuts";
import { useMessageEvents } from "@/hooks/useMessageEvents";
import { useSystemTrayBadge } from "@/hooks/useSystemTrayBadge";
import { useTranslation } from "react-i18next";

export function ChatLayout() {
  const { t } = useTranslation();
  useMessageEvents();
  useKeyboardShortcuts();
  useSystemTrayBadge();

  const selectedContact = useAppStore((state) => state.selectedContact);
  const showThreads = useAppStore((state) => state.showThreads);
  const setShowThreads = useAppStore((state) => state.setShowThreads);
  const selectedThreadId = useAppStore((state) => state.selectedThreadId);
  const setSelectedThreadId = useAppStore((state) => state.setSelectedThreadId);
  const showConversationDetails = useAppStore(
    (state) => state.showConversationDetails
  );
  const theme = useAppStore((state) => state.theme);

  const [showOnboarding, setShowOnboarding] = useState(false);
  const [showProvidersModal, setShowProvidersModal] = useState(false);
  const [isSearchOpen, setIsSearchOpen] = useState(false);
  const [isSettingsOpen, setIsSettingsOpen] = useState(false);
  const [isSyncing, setIsSyncing] = useState(false);

  const checkProviders = useCallback(async () => {
    try {
      const providers = await GetConfiguredProviders();
      const hasConfiguredProviders = providers && providers.length > 0;
      setShowOnboarding(!hasConfiguredProviders && !isSyncing);
    } catch (error) {
      console.error("Failed to check providers:", error);
      setShowOnboarding(false);
    }
  }, [isSyncing]);

  useEffect(() => {
    checkProviders();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // ⌘K / Ctrl+K search shortcut
  useEffect(() => {
    if (showOnboarding) return;
    const handleKeyDown = (e: KeyboardEvent) => {
      const isMac = navigator.platform.toUpperCase().includes("MAC");
      if ((isMac ? e.metaKey : e.ctrlKey) && e.key === "k") {
        e.preventDefault();
        setIsSearchOpen((prev) => !prev);
      }
    };
    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, [showOnboarding]);

  useEffect(() => {
    let timeoutId: ReturnType<typeof setTimeout> | null = null;
    const unsubscribe = EventsOn("contacts-refresh", () => {
      if (timeoutId) clearTimeout(timeoutId);
      timeoutId = setTimeout(() => {
        checkProviders();
      }, 2000);
    });
    return () => {
      if (timeoutId) clearTimeout(timeoutId);
      if (unsubscribe) unsubscribe();
    };
  }, [checkProviders]);

  useEffect(() => {
    const unsubscribe = EventsOn("sync-status", (statusJSON: string) => {
      try {
        const status = JSON.parse(statusJSON);
        const isActiveSync =
          status.Status === "fetching_contacts" ||
          status.Status === "fetching_history" ||
          status.Status === "fetching_avatars";
        setIsSyncing(isActiveSync);
        if (status.Status === "completed" || status.Status === "error") {
          setTimeout(async () => {
            setIsSyncing(false);
            await checkProviders();
          }, 2000);
        }
      } catch (error) {
        console.error("Failed to parse sync status:", error);
      }
    });
    return () => {
      if (unsubscribe) unsubscribe();
    };
  }, [checkProviders]);

  const handleProvidersModalClose = async (open: boolean) => {
    setShowProvidersModal(open);
    if (!open) {
      setIsSyncing(true);
      setTimeout(async () => {
        await checkProviders();
      }, 1000);
    }
  };

  const handleConfigureProvider = () => {
    setShowProvidersModal(true);
  };

  const shouldShowThreadsOverlay = showThreads && selectedThreadId !== null;
  const shouldShowDetailsPanel =
    showConversationDetails && selectedContact !== null;

  const handleCloseThread = () => {
    setShowThreads(false);
    setSelectedThreadId(null);
  };

  const getMessagesPanelSize = () => {
    if (shouldShowDetailsPanel) return 50;
    return 75;
  };

  return (
    <div className="flex flex-col h-screen">
      {/* macOS title bar drag area */}
      <div
        className="h-[44px] bg-sidebar-rail shrink-0"
        style={{ "--wails-draggable": "drag" } as React.CSSProperties}
        onDoubleClick={(event) => {
          if (event.target === event.currentTarget) WindowToggleMaximise();
        }}
      />

      {showOnboarding ? (
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
        <>
          <div className="flex flex-1 min-h-0">
            {/* Column 1: Icon rail */}
            <ProviderFilterBar
              onOpenSearch={() => setIsSearchOpen(true)}
              onOpenProviders={handleConfigureProvider}
              onOpenSettings={() => setIsSettingsOpen(true)}
            />

            {/* Columns 2+3: Sidebar + Messages */}
            <ResizablePanelGroup
              direction="horizontal"
              className="flex-1"
            >
              <ResizablePanel
                id="contacts-panel"
                defaultSize={22}
                minSize={15}
              >
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
                {/* Relative container so the thread overlay can be absolute-positioned */}
                <div className="relative h-full overflow-hidden">
                  <Suspense fallback={<MessageListSkeleton />}>
                    {selectedContact ? (
                      <MessageList selectedConversation={selectedContact} />
                    ) : (
                      <div className="h-full flex flex-col items-center justify-center text-muted-foreground">
                        <img
                          src="https://api.iconify.design/marketeq:conversation.svg"
                          className="h-16 w-16 mb-4 opacity-50"
                          style={{
                            filter:
                              theme === "dark"
                                ? "grayscale(1) invert(1) brightness(1.5)"
                                : "none",
                          }}
                          alt="Conversation icon"
                        />
                        <p className="text-xl font-medium">
                          {t("select_a_conversation")}
                        </p>
                      </div>
                    )}
                  </Suspense>

                  {/* Thread overlay — slides in from the right over the messages area */}
                  {shouldShowThreadsOverlay && (
                    <>
                      {/* Dim backdrop — click or Escape to close */}
                      <button
                        className="absolute inset-0 bg-black/25 z-10 cursor-default border-0 p-0"
                        aria-label={t("close_thread") || "Close thread"}
                        onClick={handleCloseThread}
                        onKeyDown={(e) => e.key === "Escape" && handleCloseThread()}
                      />
                      {/* Thread panel */}
                      <div className="thread-panel-enter absolute inset-y-0 right-0 z-20 w-[560px] max-w-[90%] flex flex-col bg-background shadow-2xl border-l overflow-hidden">
                        <ThreadView />
                      </div>
                    </>
                  )}
                </div>
              </ResizablePanel>
              {shouldShowDetailsPanel && (
                <>
                  <ResizableHandle withHandle />
                  <ResizablePanel
                    id="details-panel"
                    defaultSize={25}
                    minSize={15}
                  >
                    <Suspense fallback={<ConversationDetailsViewSkeleton />}>
                      <ConversationDetailsView
                        selectedConversation={selectedContact!}
                      />
                    </Suspense>
                  </ResizablePanel>
                </>
              )}
            </ResizablePanelGroup>
          </div>
          <SyncStatusFooter />
        </>
      )}

      <SearchModal open={isSearchOpen} onOpenChange={setIsSearchOpen} />
      <SettingsModal open={isSettingsOpen} onOpenChange={setIsSettingsOpen} />
      <AvatarModal />
      <ProvidersModal
        open={showProvidersModal}
        onOpenChange={handleProvidersModalClose}
      />
    </div>
  );
}
