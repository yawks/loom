import { Skeleton } from "@/components/ui/skeleton";
import { X } from "lucide-react";
import { Button } from "@/components/ui/button";

export function ConversationDetailsViewSkeleton() {
  return (
    <div className="flex flex-col h-full">
      <div className="p-4 border-b flex justify-between items-center shrink-0">
        <Skeleton className="h-5 w-32" />
        <Button variant="ghost" size="icon" disabled>
          <X className="h-4 w-4" />
        </Button>
      </div>
      <div className="flex-1 overflow-y-auto p-4 min-h-0 scroll-area">
        <div className="space-y-6">
          {/* Participants skeleton */}
          <div>
            <Skeleton className="h-4 w-24 mb-3" />
            <div className="space-y-3">
              {Array.from({ length: 5 }).map((_, index) => (
                <div key={index} className="flex items-center gap-3 p-2">
                  <div className="relative shrink-0">
                    <Skeleton className="h-10 w-10 rounded-full" />
                    <Skeleton className="absolute -bottom-0.5 -right-0.5 h-3 w-3 rounded-full" />
                  </div>
                  <div className="flex-1 min-w-0 space-y-2">
                    <div className="flex items-center gap-2">
                      <Skeleton className="h-4 w-[150px]" />
                      <Skeleton className="h-3 w-[60px] rounded" />
                    </div>
                    <div className="flex items-center gap-2">
                      <Skeleton className="h-2 w-2 rounded-full" />
                      <Skeleton className="h-3 w-[80px]" />
                    </div>
                  </div>
                </div>
              ))}
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}

