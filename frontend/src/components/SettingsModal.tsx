import { ChevronDown, MessageSquare, Monitor, Moon, Settings, Sun, Terminal, Trash2, Type, Waypoints } from "lucide-react";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { useEffect, useState } from "react";

import { Button } from "@/components/ui/button";
import { ProviderSettings } from "@/components/ProvidersModal";
import { Slider } from "@/components/ui/slider";
import { cn } from "@/lib/utils";
import i18n from "@/i18n";
import { useAppStore } from "@/lib/store";
import { useMessageReadStore } from "@/lib/messageReadStore";
import { useTranslation } from "react-i18next";

interface SettingsModalProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  initialSection?: SettingsSection;
}

export type SettingsSection = "general" | "providers";

const languages = [
  { code: "fr", name: "Français", flag: "🇫🇷" },
  { code: "en", name: "English", flag: "🇬🇧" },
] as const;

const messageLayouts = [
  { code: "bubble", name: "Messages Bubble", icon: MessageSquare },
  { code: "irc", name: "IRC", icon: Terminal },
] as const;

const themes = [
  { code: "system", name: "System Theme", icon: Monitor },
  { code: "light", name: "Light Theme", icon: Sun },
  { code: "dark", name: "Dark Theme", icon: Moon },
] as const;

export function SettingsModal({
  open,
  onOpenChange,
  initialSection = "general",
}: SettingsModalProps) {
  const { t } = useTranslation();
  const theme = useAppStore((state) => state.theme);
  const setTheme = useAppStore((state) => state.setTheme);
  const messageLayout = useAppStore((state) => state.messageLayout);
  const setMessageLayout = useAppStore((state) => state.setMessageLayout);
  const language = useAppStore((state) => state.language);
  const setLanguage = useAppStore((state) => state.setLanguage);
  const fontSize = useAppStore((state) => state.fontSize);
  const setFontSize = useAppStore((state) => state.setFontSize);
  const [isLanguagePopoverOpen, setIsLanguagePopoverOpen] = useState(false);
  const [isMessageLayoutPopoverOpen, setIsMessageLayoutPopoverOpen] = useState(false);
  const [isThemePopoverOpen, setIsThemePopoverOpen] = useState(false);
  const [activeSection, setActiveSection] = useState<SettingsSection>(initialSection);

  // Font size options: 50%, 75%, 100%, 125%, 150%
  const fontSizeOptions = [50, 75, 100, 125, 150] as const;

  // Sync language with i18n
  useEffect(() => {
    if (i18n.language !== language) {
      i18n.changeLanguage(language);
    }
  }, [language]);

  const handleLanguageChange = (lang: "en" | "fr") => {
    setLanguage(lang);
    i18n.changeLanguage(lang);
    setIsLanguagePopoverOpen(false);
  };

  const handleMessageLayoutChange = (layout: "bubble" | "irc") => {
    setMessageLayout(layout);
    setIsMessageLayoutPopoverOpen(false);
  };

  const handleThemeChange = (newTheme: "light" | "dark" | "system") => {
    setTheme(newTheme);
    setIsThemePopoverOpen(false);
  };

  const currentLanguage = languages.find((lang) => lang.code === language) || languages[0];
  const currentMessageLayout = messageLayouts.find((layout) => layout.code === messageLayout) || messageLayouts[0];
  const currentTheme = themes.find((themeOption) => themeOption.code === theme) || themes[0];

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="h-[min(90vh,760px)] max-w-5xl gap-0 overflow-hidden p-0 flex flex-col">
        <DialogHeader className="border-b px-6 py-5">
          <DialogTitle>{t("settings") || "Settings"}</DialogTitle>
        </DialogHeader>
        <div className="flex min-h-0 flex-1 flex-col md:flex-row">
          <nav
            aria-label={t("settings") || "Settings"}
            className="flex shrink-0 gap-1 overflow-x-auto border-b bg-muted/20 p-2 md:w-48 md:flex-col md:overflow-visible md:border-b-0 md:border-r md:p-3"
          >
            <button
              type="button"
              onClick={() => setActiveSection("providers")}
              className={cn(
                "flex min-w-max items-center gap-2 rounded-md px-3 py-2 text-left text-sm font-medium transition-colors",
                activeSection === "providers"
                  ? "bg-background text-foreground shadow-sm"
                  : "text-muted-foreground hover:bg-muted hover:text-foreground"
              )}
              aria-current={activeSection === "providers" ? "page" : undefined}
            >
              <Waypoints className="h-4 w-4" />
              {t("providers")}
            </button>
            <button
              type="button"
              onClick={() => setActiveSection("general")}
              className={cn(
                "flex min-w-max items-center gap-2 rounded-md px-3 py-2 text-left text-sm font-medium transition-colors",
                activeSection === "general"
                  ? "bg-background text-foreground shadow-sm"
                  : "text-muted-foreground hover:bg-muted hover:text-foreground"
              )}
              aria-current={activeSection === "general" ? "page" : undefined}
            >
              <Settings className="h-4 w-4" />
              {t("settings_general")}
            </button>
          </nav>

          {activeSection === "providers" ? (
            <div className="flex min-h-0 flex-1 p-6">
              <ProviderSettings open={open} onOpenChange={onOpenChange} />
            </div>
          ) : (
          <div className="space-y-6 overflow-y-auto flex-1 min-h-0 scroll-area p-6">
          <div className="space-y-3">
            <div className="text-sm font-semibold">
              {t("message_layout")}
            </div>
            <Popover open={isMessageLayoutPopoverOpen} onOpenChange={setIsMessageLayoutPopoverOpen}>
              <PopoverTrigger asChild>
                <Button
                  variant="outline"
                  className="w-full justify-between"
                >
                  <span className="flex items-center">
                    {(() => {
                      const Icon = currentMessageLayout.icon;
                      return <Icon className="mr-2 h-4 w-4" />;
                    })()}
                    {t(currentMessageLayout.code === "bubble" ? "messages_bubble" : "irc")}
                  </span>
                  <ChevronDown className="h-4 w-4 opacity-50" />
                </Button>
              </PopoverTrigger>
              <PopoverContent className="w-[var(--radix-popover-trigger-width)] p-1" align="start">
                <div className="space-y-1">
                  {messageLayouts.map((layout) => {
                    const Icon = layout.icon;
                    return (
                      <Button
                        key={layout.code}
                        variant={messageLayout === layout.code ? "secondary" : "ghost"}
                        className="w-full justify-start"
                        onClick={() => handleMessageLayoutChange(layout.code as "bubble" | "irc")}
                      >
                        <Icon className="mr-2 h-4 w-4" />
                        {t(layout.code === "bubble" ? "messages_bubble" : "irc")}
                        {messageLayout === layout.code && (
                          <span className="ml-auto text-xs">✓</span>
                        )}
                      </Button>
                    );
                  })}
                </div>
              </PopoverContent>
            </Popover>
          </div>
          <div className="border-t" />
          <div className="space-y-3">
            <div className="text-sm font-semibold">
              {t("theme") || "Theme"}
            </div>
            <Popover open={isThemePopoverOpen} onOpenChange={setIsThemePopoverOpen}>
              <PopoverTrigger asChild>
                <Button
                  variant="outline"
                  className="w-full justify-between"
                >
                  <span className="flex items-center">
                    {(() => {
                      const Icon = currentTheme.icon;
                      return <Icon className="mr-2 h-4 w-4" />;
                    })()}
                    {t(`${currentTheme.code}_theme`)}
                  </span>
                  <ChevronDown className="h-4 w-4 opacity-50" />
                </Button>
              </PopoverTrigger>
              <PopoverContent className="w-[var(--radix-popover-trigger-width)] p-1" align="start">
                <div className="space-y-1">
                  {themes.map((themeOption) => {
                    const Icon = themeOption.icon;
                    return (
                      <Button
                        key={themeOption.code}
                        variant={theme === themeOption.code ? "secondary" : "ghost"}
                        className="w-full justify-start"
                        onClick={() => handleThemeChange(themeOption.code)}
                      >
                        <Icon className="mr-2 h-4 w-4" />
                        {t(`${themeOption.code}_theme`)}
                        {theme === themeOption.code && (
                          <span className="ml-auto text-xs">✓</span>
                        )}
                      </Button>
                    );
                  })}
                </div>
              </PopoverContent>
            </Popover>
          </div>
          <div className="border-t" />
          <div className="space-y-3">
            <div className="text-sm font-semibold">
              {t("language") || "Language"}
            </div>
            <Popover open={isLanguagePopoverOpen} onOpenChange={setIsLanguagePopoverOpen}>
              <PopoverTrigger asChild>
                <Button
                  variant="outline"
                  className="w-full justify-between"
                >
                  <span className="flex items-center">
                    <span className="mr-2 text-lg">{currentLanguage.flag}</span>
                    {currentLanguage.name}
                  </span>
                  <ChevronDown className="h-4 w-4 opacity-50" />
                </Button>
              </PopoverTrigger>
              <PopoverContent className="w-[var(--radix-popover-trigger-width)] p-1" align="start">
                <div className="space-y-1">
                  {languages.map((lang) => (
                    <Button
                      key={lang.code}
                      variant={language === lang.code ? "secondary" : "ghost"}
                      className="w-full justify-start"
                      onClick={() => handleLanguageChange(lang.code as "en" | "fr")}
                    >
                      <span className="mr-2 text-lg">{lang.flag}</span>
                      {lang.name}
                      {language === lang.code && (
                        <span className="ml-auto text-xs">✓</span>
                      )}
                    </Button>
                  ))}
                </div>
              </PopoverContent>
            </Popover>
          </div>
          <div className="border-t" />
          <div className="space-y-3">
            <div className="text-sm font-semibold">
              {t("font_size") || "Font Size"}
            </div>
            <div className="space-y-2">
              <div className="flex items-center gap-2">
                <Type className="h-4 w-4 text-muted-foreground" />
                <Slider
                  value={fontSize}
                  onValueChange={setFontSize}
                  min={50}
                  max={150}
                  step={25}
                  className="flex-1"
                />
                <span className="text-sm font-medium min-w-[3rem] text-right">
                  {fontSize}%
                </span>
              </div>
              <div className="flex justify-between text-xs text-muted-foreground px-1">
                {fontSizeOptions.map((size) => (
                  <button
                    key={size}
                    onClick={() => setFontSize(size)}
                    className={cn(
                      "px-1 py-0.5 rounded transition-colors",
                      fontSize === size
                        ? "text-primary font-medium"
                        : "hover:text-foreground"
                    )}
                  >
                    {size}%
                  </button>
                ))}
              </div>
            </div>
          </div>
          <div className="border-t" />
          <div className="space-y-3">
            <div className="text-sm font-semibold">
              {t("keyboard_shortcuts") || "Keyboard Shortcuts"}
            </div>
            <div className="space-y-2 text-sm">
              <div className="flex items-center justify-between py-1">
                <span className="text-muted-foreground">
                  {t("shortcut_navigate_unread_up") || "Navigate to unread conversation above"}
                </span>
                <kbd className="inline-flex h-5 select-none items-center gap-1 rounded border bg-muted px-1.5 font-mono text-[10px] font-medium text-muted-foreground">
                  {navigator.platform.toUpperCase().indexOf("MAC") >= 0 ? "⌥" : "Alt"}
                  <span className="text-xs">+</span>
                  <span className="text-xs">⇧</span>
                  <span className="text-xs">+</span>
                  <span className="text-xs">↑</span>
                </kbd>
              </div>
              <div className="flex items-center justify-between py-1">
                <span className="text-muted-foreground">
                  {t("shortcut_navigate_unread_down") || "Navigate to unread conversation below"}
                </span>
                <kbd className="inline-flex h-5 select-none items-center gap-1 rounded border bg-muted px-1.5 font-mono text-[10px] font-medium text-muted-foreground">
                  {navigator.platform.toUpperCase().indexOf("MAC") >= 0 ? "⌥" : "Alt"}
                  <span className="text-xs">+</span>
                  <span className="text-xs">⇧</span>
                  <span className="text-xs">+</span>
                  <span className="text-xs">↓</span>
                </kbd>
              </div>
              <div className="flex items-center justify-between py-1">
                <span className="text-muted-foreground">
                  {t("shortcut_navigate_history_back") || "Previous conversation in history"}
                </span>
                <kbd className="inline-flex h-5 select-none items-center gap-1 rounded border bg-muted px-1.5 font-mono text-[10px] font-medium text-muted-foreground">
                  {navigator.platform.toUpperCase().indexOf("MAC") >= 0 ? "⌃" : "Ctrl"}
                  <span className="text-xs">+</span>
                  <span className="text-xs">⇧</span>
                  <span className="text-xs">+</span>
                  <span className="text-xs">←</span>
                </kbd>
              </div>
              <div className="flex items-center justify-between py-1">
                <span className="text-muted-foreground">
                  {t("shortcut_navigate_history_forward") || "Next conversation in history"}
                </span>
                <kbd className="inline-flex h-5 select-none items-center gap-1 rounded border bg-muted px-1.5 font-mono text-[10px] font-medium text-muted-foreground">
                  {navigator.platform.toUpperCase().indexOf("MAC") >= 0 ? "⌃" : "Ctrl"}
                  <span className="text-xs">+</span>
                  <span className="text-xs">⇧</span>
                  <span className="text-xs">+</span>
                  <span className="text-xs">→</span>
                </kbd>
              </div>
              <div className="flex items-center justify-between py-1">
                <span className="text-muted-foreground">
                  {t("shortcut_navigate_list_up") || "Navigate to conversation above in list"}
                </span>
                <kbd className="inline-flex h-5 select-none items-center gap-1 rounded border bg-muted px-1.5 font-mono text-[10px] font-medium text-muted-foreground">
                  {navigator.platform.toUpperCase().indexOf("MAC") >= 0 ? "⌥" : "Alt"}
                  <span className="text-xs">+</span>
                  <span className="text-xs">↑</span>
                </kbd>
              </div>
              <div className="flex items-center justify-between py-1">
                <span className="text-muted-foreground">
                  {t("shortcut_navigate_list_down") || "Navigate to conversation below in list"}
                </span>
                <kbd className="inline-flex h-5 select-none items-center gap-1 rounded border bg-muted px-1.5 font-mono text-[10px] font-medium text-muted-foreground">
                  {navigator.platform.toUpperCase().indexOf("MAC") >= 0 ? "⌥" : "Alt"}
                  <span className="text-xs">+</span>
                  <span className="text-xs">↓</span>
                </kbd>
              </div>
            </div>
          </div>
          <div className="border-t" />
          <div className="space-y-3">
            <div className="text-sm font-semibold">
              {t("data_management") || "Data Management"}
            </div>
            <Button
              variant="outline"
              className="w-full justify-start text-destructive hover:text-destructive hover:bg-destructive/10"
              onClick={() => {
                if (window.confirm(t("clear_read_state_confirm") || "This will reset all message read states. Continue?")) {
                  // Clear all conversations from the read store
                  const readStore = useMessageReadStore.getState();
                  const conversations = Object.keys(readStore.readByConversation);
                  conversations.forEach((convId) => {
                    readStore.clearConversation(convId);
                  });
                  // Also clear localStorage directly
                  if (typeof window !== "undefined") {
                    window.localStorage.removeItem("loom-message-read-state");
                  }
                  // Reload the page to refresh the UI
                  window.location.reload();
                }
              }}
            >
              <Trash2 className="mr-2 h-4 w-4" />
              {t("clear_read_state") || "Clear Read State Cache"}
            </Button>
            <p className="text-xs text-muted-foreground">
              {t("clear_read_state_description") || "Reset all message read states. This will not delete your messages."}
            </p>
          </div>
          </div>
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}
