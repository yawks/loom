import { Skeleton } from "@/components/ui/skeleton";

export function MessageListSkeleton() {
  return (
    <div className="flex flex-col space-y-4 p-4">
      <div className="flex items-start space-x-4">
        <Skeleton className="h-10 w-10 rounded-full" />
        <div className="space-y-2">
          <Skeleton className="h-4 w-[200px]" />
          <Skeleton className="h-4 w-[150px]" />
        </div>
      </div>
      <div className="flex items-start justify-end space-x-4">
        <div className="space-y-2 text-right">
          <Skeleton className="h-4 w-[200px]" />
        </div>
        <Skeleton className="h-10 w-10 rounded-full" />
      </div>
      <div className="flex items-start space-x-4">
        <Skeleton className="h-10 w-10 rounded-full" />
        <div className="space-y-2">
          <Skeleton className="h-4 w-[250px]" />
        </div>
      </div>
    </div>
  );
}
