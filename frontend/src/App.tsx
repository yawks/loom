import { useEffect } from "react";
import { ChatLayout } from "@/components/ChatLayout";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { useAppStore } from "@/lib/store";
import "./App.css";

const queryClient = new QueryClient();

function AppContent() {
  const theme = useAppStore((state) => state.theme);

  useEffect(() => {
    // Apply theme to document on mount
    const root = document.documentElement;
    if (theme === "dark") {
      root.classList.add("dark");
    } else {
      root.classList.remove("dark");
    }
  }, [theme]);

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
