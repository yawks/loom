import { useEffect } from "react";
import { ChatLayout } from "@/components/ChatLayout";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { useAppStore } from "@/lib/store";
import i18n from "@/i18n";
import "./App.css";

const queryClient = new QueryClient();

function AppContent() {
  const theme = useAppStore((state) => state.theme);
  const language = useAppStore((state) => state.language);

  useEffect(() => {
    // Apply theme to document on mount
    const root = document.documentElement;
    if (theme === "dark") {
      root.classList.add("dark");
    } else {
      root.classList.remove("dark");
    }
  }, [theme]);

  useEffect(() => {
    // Initialize language on mount
    if (i18n.language !== language) {
      i18n.changeLanguage(language);
    }
  }, [language]);

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
