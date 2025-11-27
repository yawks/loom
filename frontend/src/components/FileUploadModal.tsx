import { useState, useRef, useEffect } from "react";
import { useTranslation } from "react-i18next";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { X, File } from "lucide-react";

interface FileUploadModalProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  files: File[];
  filePaths?: string[]; // Optional file paths for clipboard/drag&drop files in Wails
  onConfirm: (files: File[], filePaths?: string[]) => void;
}

export function FileUploadModal({
  open,
  onOpenChange,
  files,
  filePaths,
  onConfirm,
}: FileUploadModalProps) {
  const { t } = useTranslation();
  const [selectedFiles, setSelectedFiles] = useState<File[]>(files);
  const [selectedFilePaths, setSelectedFilePaths] = useState<string[]>(filePaths ?? []);
  const [imagePreviews, setImagePreviews] = useState<Record<string, string>>({});
  const fileInputRef = useRef<HTMLInputElement>(null);

  // Sync selectedFiles with files prop when it changes
  useEffect(() => {
    if (files.length > 0) {
      setSelectedFiles(files);
    }
  }, [files]);

  // Sync file paths when prop changes
  useEffect(() => {
    if (filePaths !== undefined) {
      setSelectedFilePaths(filePaths);
    }
  }, [filePaths]);

  useEffect(() => {
    const previews: Record<string, string> = {};
    selectedFiles.forEach((file) => {
      if (file.type?.startsWith("image/")) {
        const key = `${file.name}-${file.size}-${file.lastModified}`;
        previews[key] = URL.createObjectURL(file);
      }
    });
    setImagePreviews(previews);

    return () => {
      Object.values(previews).forEach((url) => URL.revokeObjectURL(url));
    };
  }, [selectedFiles]);

  const handleRemoveFile = (index: number) => {
    setSelectedFiles((prev) => prev.filter((_, i) => i !== index));
  };

  const handleAddMoreFiles = () => {
    fileInputRef.current?.click();
  };

  const handleFileInputChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    if (e.target.files) {
      const newFiles = Array.from(e.target.files);
      setSelectedFiles((prev) => [...prev, ...newFiles]);
    }
  };

  const handleConfirm = () => {
    if (selectedFiles.length > 0 || selectedFilePaths.length > 0) {
      onConfirm(selectedFiles, selectedFilePaths.length > 0 ? selectedFilePaths : undefined);
      setSelectedFiles([]);
      setSelectedFilePaths([]);
      onOpenChange(false);
    }
  };

  useEffect(() => {
    if (!open) {
      return;
    }

    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Enter") {
        const hasSelection = selectedFiles.length > 0 || selectedFilePaths.length > 0;
        if (hasSelection) {
          event.preventDefault();
          handleConfirm();
        }
      }
    };

    window.addEventListener("keydown", handleKeyDown);
    return () => {
      window.removeEventListener("keydown", handleKeyDown);
    };
  }, [open, selectedFiles, selectedFilePaths]);

  const handleCancel = () => {
    setSelectedFiles([]);
    setSelectedFilePaths([]);
    onOpenChange(false);
  };

  const formatFileSize = (bytes: number): string => {
    if (bytes === 0) return "0 Bytes";
    const k = 1024;
    const sizes = ["Bytes", "KB", "MB", "GB"];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return Math.round(bytes / Math.pow(k, i) * 100) / 100 + " " + sizes[i];
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t("upload_files")}</DialogTitle>
          <DialogDescription>{t("upload_files_description")}</DialogDescription>
        </DialogHeader>
        <div className="space-y-4">
          <div className="max-h-60 overflow-y-auto space-y-2">
            {selectedFiles.length === 0 && selectedFilePaths.length === 0 ? (
              <p className="text-sm text-muted-foreground text-center py-4">
                {t("no_files_selected")}
              </p>
            ) : (
              <>
                {selectedFiles.map((file, index) => (
                  <div
                    key={`file-${file.name}-${index}`}
                    className="flex items-center gap-3 p-3 border rounded-lg"
                  >
                    {file.type?.startsWith("image/") ? (
                      <img
                        src={imagePreviews[`${file.name}-${file.size}-${file.lastModified}`]}
                        alt={file.name}
                        className="h-10 w-10 rounded object-cover border"
                      />
                    ) : (
                      <File className="h-5 w-5 text-muted-foreground shrink-0" />
                    )}
                    <div className="flex-1 min-w-0">
                      <p className="text-sm font-medium truncate">{file.name}</p>
                      <p className="text-xs text-muted-foreground">
                        {formatFileSize(file.size)}
                      </p>
                    </div>
                    <Button
                      variant="ghost"
                      size="icon"
                      className="h-8 w-8 shrink-0"
                      onClick={() => handleRemoveFile(index)}
                    >
                      <X className="h-4 w-4" />
                    </Button>
                  </div>
                ))}
                {selectedFilePaths.map((filePath, index) => (
                  <div
                    key={`path-${filePath}-${index}`}
                    className="flex items-center gap-3 p-3 border rounded-lg"
                  >
                    <File className="h-5 w-5 text-muted-foreground shrink-0" />
                    <div className="flex-1 min-w-0">
                      <p className="text-sm font-medium truncate">{filePath.split(/[/\\]/).pop() || filePath}</p>
                      <p className="text-xs text-muted-foreground truncate">
                        {filePath}
                      </p>
                    </div>
                    <Button
                      variant="ghost"
                      size="icon"
                      className="h-8 w-8 shrink-0"
                      onClick={() => setSelectedFilePaths(prev => prev.filter((_, i) => i !== index))}
                    >
                      <X className="h-4 w-4" />
                    </Button>
                  </div>
                ))}
              </>
            )}
          </div>
          <Button
            variant="outline"
            onClick={handleAddMoreFiles}
            className="w-full"
          >
            {t("add_more_files")}
          </Button>
          <input
            ref={fileInputRef}
            type="file"
            multiple
            className="hidden"
            onChange={handleFileInputChange}
          />
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={handleCancel}>
            {t("cancel")}
          </Button>
          <Button onClick={handleConfirm} disabled={selectedFiles.length === 0 && selectedFilePaths.length === 0}>
            {t("upload")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

