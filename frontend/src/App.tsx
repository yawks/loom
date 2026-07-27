import { useEffect } from "react";
import { ChatLayout } from "@/components/ChatLayout";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { useAppStore } from "@/lib/store";
import i18n from "@/i18n";
import "./App.css";

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      // Conversations are frequently revisited, but keeping every inactive
      // history in memory for the whole session is unnecessarily expensive.
      staleTime: 30_000,
      gcTime: 2 * 60_000,
      refetchOnWindowFocus: false,
    },
  },
});

function AppContent() {
  const theme = useAppStore((state) => state.theme);
  const language = useAppStore((state) => state.language);
  const fontSize = useAppStore((state) => state.fontSize);

  useEffect(() => {
    const root = document.documentElement;
    const applyTheme = (isDark: boolean) => {
      root.classList.toggle("dark", isDark);
    };

    if (theme === "system") {
      const mq = globalThis.matchMedia("(prefers-color-scheme: dark)");
      applyTheme(mq.matches);
      const handler = (e: MediaQueryListEvent) => applyTheme(e.matches);
      mq.addEventListener("change", handler);
      return () => mq.removeEventListener("change", handler);
    } else {
      applyTheme(theme === "dark");
    }
  }, [theme]);

  useEffect(() => {
    // Initialize language on mount
    if (i18n.language !== language) {
      i18n.changeLanguage(language);
    }
  }, [language]);

  useEffect(() => {
    // Apply font size to document
    const root = document.documentElement;
    root.style.fontSize = `${fontSize}%`;
  }, [fontSize]);

  return (
    <main className="h-screen overflow-hidden">
      <ChatLayout />
    </main>
  );
}

function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <AppContent />
    </QueryClientProvider>
  );
}

export default App;
