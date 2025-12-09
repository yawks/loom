import { Skeleton } from "@/components/ui/skeleton";

export function ParticipantListSkeleton() {
  return (
    <div className="space-y-3">
      {/* Render 5 skeleton items */}
      {Array.from({ length: 5 }).map((_, index) => (
        <div key={index} className="flex items-center gap-3 p-2">
          <div className="relative shrink-0">
            <Skeleton className="h-10 w-10 rounded-full" />
            {/* Status indicator skeleton */}
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
  );
}

