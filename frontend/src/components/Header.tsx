import { useEffect, useState } from "react";
import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import { Button } from "@/components/ui/button";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
import { Moon, Sun, LogOut, User, MessageSquare, Terminal, Layers } from "lucide-react";
import { useTranslation } from "react-i18next";
import { useAppStore } from "@/lib/store";
import { ProvidersModal } from "@/components/ProvidersModal";

export function Header() {
  const { t } = useTranslation();
  const theme = useAppStore((state) => state.theme);
  const setTheme = useAppStore((state) => state.setTheme);
  const messageLayout = useAppStore((state) => state.messageLayout);
  const setMessageLayout = useAppStore((state) => state.setMessageLayout);
  const [isProvidersOpen, setIsProvidersOpen] = useState(false);

  useEffect(() => {
    // Apply theme to document
    const root = document.documentElement;
    if (theme === "dark") {
      root.classList.add("dark");
    } else {
      root.classList.remove("dark");
    }
  }, [theme]);

  const handleThemeToggle = () => {
    setTheme(theme === "dark" ? "light" : "dark");
  };

  const handleDisconnect = () => {
    // TODO: Implement disconnect logic
    console.log("Disconnect clicked");
  };

  return (
    <header className="h-14 border-b flex items-center justify-end px-4 bg-background">
      <ProvidersModal open={isProvidersOpen} onOpenChange={setIsProvidersOpen} />
      <Popover>
        <PopoverTrigger asChild>
          <Button variant="ghost" size="icon" className="rounded-full">
            <Avatar className="h-8 w-8">
              <AvatarImage src="" />
              <AvatarFallback>
                <User className="h-4 w-4" />
              </AvatarFallback>
            </Avatar>
          </Button>
        </PopoverTrigger>
        <PopoverContent align="end" className="w-56">
          <div className="space-y-2">
            <div className="px-2 py-1.5 text-sm font-semibold">Profile</div>
            <div className="space-y-1">
              <div className="px-2 py-1.5 text-xs font-semibold text-muted-foreground">
                {t("message_layout")}
              </div>
              <Button
                variant="ghost"
                className="w-full justify-start"
                onClick={() => setMessageLayout("bubble")}
              >
                <MessageSquare className={`mr-2 h-4 w-4 ${messageLayout === "bubble" ? "opacity-100" : "opacity-50"}`} />
                {t("messages_bubble")}
                {messageLayout === "bubble" && (
                  <span className="ml-auto text-xs">✓</span>
                )}
              </Button>
              <Button
                variant="ghost"
                className="w-full justify-start"
                onClick={() => setMessageLayout("irc")}
              >
                <Terminal className={`mr-2 h-4 w-4 ${messageLayout === "irc" ? "opacity-100" : "opacity-50"}`} />
                {t("irc")}
                {messageLayout === "irc" && (
                  <span className="ml-auto text-xs">✓</span>
                )}
              </Button>
              <div className="border-t my-1" />
              <Button
                variant="ghost"
                className="w-full justify-start"
                onClick={() => setIsProvidersOpen(true)}
              >
                <Layers className="mr-2 h-4 w-4" />
                Providers
              </Button>
              <Button
                variant="ghost"
                className="w-full justify-start"
                onClick={handleThemeToggle}
              >
                {theme === "dark" ? (
                  <>
                    <Sun className="mr-2 h-4 w-4" />
                    Light Theme
                  </>
                ) : (
                  <>
                    <Moon className="mr-2 h-4 w-4" />
                    Dark Theme
                  </>
                )}
              </Button>
              <Button
                variant="ghost"
                className="w-full justify-start text-destructive hover:text-destructive"
                onClick={handleDisconnect}
              >
                <LogOut className="mr-2 h-4 w-4" />
                {t("disconnect") || "Disconnect"}
              </Button>
            </div>
          </div>
        </PopoverContent>
      </Popover>
    </header>
  );
}

