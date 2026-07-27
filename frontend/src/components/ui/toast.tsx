import { X } from "lucide-react";
import { useEffect, useState } from "react";
import { cn } from "@/lib/utils";

export interface Toast {
  id: string;
  message: string;
  type?: "error" | "success" | "info";
  action?: { label: string; onClick: () => void };
}

interface ToastProps {
  toast: Toast;
  onClose: (id: string) => void;
}

function ToastItem({ toast, onClose }: ToastProps) {
  useEffect(() => {
    const timer = setTimeout(() => {
      onClose(toast.id);
    }, 5000);

    return () => clearTimeout(timer);
  }, [toast.id, onClose]);

  return (
    <div
      className={cn(
        "flex items-center gap-3 rounded-lg border p-4 shadow-lg bg-background text-foreground",
        toast.type === "error" && "border-destructive bg-red-100 dark:bg-red-950 text-destructive",
        toast.type === "success" && "border-green-500 bg-green-100 dark:bg-green-950 text-green-700 dark:text-green-400",
        toast.type === "info" && "border-blue-500 bg-blue-100 dark:bg-blue-950 text-blue-700 dark:text-blue-400"
      )}
    >
      <p className="text-sm font-medium">{toast.message}</p>
      {toast.action && (
        <button
          onClick={() => { toast.action!.onClick(); onClose(toast.id); }}
          className="ml-2 shrink-0 text-sm font-semibold underline hover:no-underline"
        >
          {toast.action.label}
        </button>
      )}
      <button
        onClick={() => onClose(toast.id)}
        className="ml-auto rounded-md p-1 hover:bg-black/10 dark:hover:bg-white/10"
      >
        <X className="h-4 w-4" />
      </button>
    </div>
  );
}

export function ToastContainer({ toasts, onClose }: { toasts: Toast[]; onClose: (id: string) => void }) {
  if (toasts.length === 0) {
    return null;
  }

  return (
    <div className="fixed top-4 right-4 z-[100] flex flex-col gap-2 max-w-md">
      {toasts.map((toast) => (
        <ToastItem key={toast.id} toast={toast} onClose={onClose} />
      ))}
    </div>
  );
}

let toastIdCounter = 0;

export function useToast() {
  const [toasts, setToasts] = useState<Toast[]>([]);

  const showToast = (message: string, type: Toast["type"] = "info", action?: Toast["action"]) => {
    const id = `toast-${++toastIdCounter}`;
    setToasts((prev) => [...prev, { id, message, type, action }]);
    return id;
  };

  const closeToast = (id: string) => {
    setToasts((prev) => prev.filter((toast) => toast.id !== id));
  };

  return { toasts, showToast, closeToast };
}







