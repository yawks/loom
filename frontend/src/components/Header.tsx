import { Layers, Search, Settings } from "lucide-react";
import { useEffect, useState } from "react";

import { Button } from "@/components/ui/button";
import { ProvidersModal } from "@/components/ProvidersModal";
import { SearchModal } from "@/components/SearchModal";
import { SettingsModal } from "@/components/SettingsModal";
import { useAppStore } from "@/lib/store";
import { useTranslation } from "react-i18next";

interface HeaderProps {
  hasProviders?: boolean;
}

export function Header({ hasProviders = true }: HeaderProps) {
  const { t } = useTranslation();
  const theme = useAppStore((state) => state.theme);
  const [isProvidersOpen, setIsProvidersOpen] = useState(false);
  const [isSearchOpen, setIsSearchOpen] = useState(false);
  const [isSettingsOpen, setIsSettingsOpen] = useState(false);

  useEffect(() => {
    // Apply theme to document
    const root = document.documentElement;
    if (theme === "dark") {
      root.classList.add("dark");
    } else {
      root.classList.remove("dark");
    }
  }, [theme]);

  // Handle keyboard shortcut for search (Ctrl+K / Cmd+K)
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      // Only enable search shortcut if providers are configured
      if (!hasProviders) return;

      const isMac = navigator.platform.toUpperCase().indexOf("MAC") >= 0;
      const modifierKey = isMac ? e.metaKey : e.ctrlKey;

      if (modifierKey && e.key === "k") {
        e.preventDefault();
        setIsSearchOpen((prev) => !prev);
      }
    };

    window.addEventListener("keydown", handleKeyDown);
    return () => {
      window.removeEventListener("keydown", handleKeyDown);
    };
  }, [hasProviders]);

  return (
    <header className="h-16 border-b flex items-center justify-between px-4 bg-background">
      <ProvidersModal open={isProvidersOpen} onOpenChange={setIsProvidersOpen} />
      <SearchModal open={isSearchOpen} onOpenChange={setIsSearchOpen} />
      <SettingsModal open={isSettingsOpen} onOpenChange={setIsSettingsOpen} />
      <div className="flex-1 flex justify-center">
        {hasProviders && (
          <Button
            variant="outline"
            size="sm"
            className="flex items-center gap-2 text-muted-foreground hover:text-foreground border-input max-w-md w-full justify-start"
            onClick={() => setIsSearchOpen(true)}
          >
            <Search className="h-4 w-4" />
            <span className="hidden sm:inline">{t("search_placeholder")}</span>
            <kbd className="hidden sm:inline-flex h-5 select-none items-center gap-1 rounded border bg-muted px-1.5 font-mono text-[10px] font-medium text-muted-foreground opacity-100 ml-auto">
              <span className="text-xs">
                {navigator.platform.toUpperCase().indexOf("MAC") >= 0 ? "⌘" : "Ctrl"}
              </span>
              K
            </kbd>
          </Button>
        )}
      </div>
      <div className="flex items-center gap-2">
        {hasProviders && (
          <Button
            variant="ghost"
            size="sm"
            className="flex items-center gap-2"
            onClick={() => setIsProvidersOpen(true)}
          >
            <Layers className="h-4 w-4" />
            <span className="hidden sm:inline">{t("providers")}</span>
          </Button>
        )}
        <Button
          variant="ghost"
          size="sm"
          className="flex items-center gap-2"
          onClick={() => setIsSettingsOpen(true)}
        >
          <Settings className="h-4 w-4" />
          <span className="hidden sm:inline">{t("settings") || "Settings"}</span>
        </Button>
      </div>
    </header>
  );
}

