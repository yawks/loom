import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";

import { useAppStore } from "@/lib/store";

export function AvatarModal() {
  const selectedAvatarUrl = useAppStore((state) => state.selectedAvatarUrl);
  const setSelectedAvatarUrl = useAppStore((state) => state.setSelectedAvatarUrl);

  return (
    <Dialog open={selectedAvatarUrl !== null} onOpenChange={(open) => {
      if (!open) {
        setSelectedAvatarUrl(null);
      }
    }}>
      <DialogContent className="max-w-2xl p-0">
        <DialogHeader className="sr-only">
          <DialogTitle>Avatar Preview</DialogTitle>
        </DialogHeader>
        {selectedAvatarUrl && (
          <div className="flex items-center justify-center p-8">
            <img
              src={selectedAvatarUrl}
              alt="Avatar"
              className="max-w-full max-h-[80vh] object-contain rounded-lg"
              onError={(e) => {
                // Hide image on error
                (e.target as HTMLImageElement).style.display = "none";
              }}
            />
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}








