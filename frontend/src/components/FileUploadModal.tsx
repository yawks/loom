import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { File, Loader2, X } from "lucide-react";
import { useEffect, useRef, useState } from "react";

import { Button } from "@/components/ui/button";
import { Progress } from "@/components/ui/progress";
import type { UploadState } from "@/hooks/useFileUpload";
import { useTranslation } from "react-i18next";

interface FileUploadModalProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  files: File[];
  filePaths?: string[]; // Optional file paths for clipboard/drag&drop files in Wails
  uploadState?: UploadState;
  onConfirm: (files: File[], filePaths?: string[]) => void;
  onUploadComplete?: () => void;
}

export function FileUploadModal({
  open,
  onOpenChange,
  files,
  filePaths,
  uploadState,
  onConfirm,
  onUploadComplete,
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

  const isUploading = Boolean(uploadState?.isUploading);

  const handleConfirm = () => {
    if (selectedFiles.length > 0 || selectedFilePaths.length > 0) {
      onConfirm(selectedFiles, selectedFilePaths.length > 0 ? selectedFilePaths : undefined);
      if (!uploadState) {
        setSelectedFiles([]);
        setSelectedFilePaths([]);
        onOpenChange(false);
      }
    }
  };

  const wasUploading = useRef(false);
  useEffect(() => {
    if (isUploading) {
      wasUploading.current = true;
    } else if (wasUploading.current) {
      wasUploading.current = false;
      setSelectedFiles([]);
      setSelectedFilePaths([]);
      onOpenChange(false);
      if (!uploadState?.error) {
        requestAnimationFrame(() => onUploadComplete?.());
      }
    }
  }, [isUploading, onOpenChange, onUploadComplete, uploadState?.error]);

  useEffect(() => {
    if (!open || isUploading) {
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
  }, [open, selectedFiles, selectedFilePaths, isUploading]);

  const handleCancel = () => {
    if (isUploading) return;
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
    <Dialog open={open} onOpenChange={(val) => { if (!isUploading) onOpenChange(val); }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t("upload_files")}</DialogTitle>
          <DialogDescription>{t("upload_files_description")}</DialogDescription>
        </DialogHeader>
        <div className="space-y-4 min-w-0">
          {isUploading && uploadState && (
            <div className="space-y-2 p-3 bg-muted/40 border rounded-lg">
              <div className="flex items-center justify-between text-xs text-muted-foreground font-medium">
                <span className="flex items-center gap-1.5 min-w-0 truncate">
                  <Loader2 className="h-3.5 w-3.5 animate-spin text-primary shrink-0" />
                  <span className="truncate">
                    {uploadState.statusText === "compressing"
                      ? t("compressing_image")
                      : t("uploading_file_progress", {
                          current: uploadState.currentFileIndex,
                          total: uploadState.totalFiles,
                        })}
                  </span>
                </span>
                <span className="shrink-0 font-mono ml-2">{uploadState.progressPercent}%</span>
              </div>
              <Progress value={uploadState.progressPercent} className="h-2" />
              {uploadState.currentFileName && (
                <p className="text-[11px] text-muted-foreground/70 truncate" title={uploadState.currentFileName}>
                  {uploadState.currentFileName}
                </p>
              )}
            </div>
          )}
          <div className="max-h-60 overflow-y-auto overflow-x-hidden min-w-0 w-full space-y-2">
            {selectedFiles.length === 0 && selectedFilePaths.length === 0 ? (
              <p className="text-sm text-muted-foreground text-center py-4">
                {t("no_files_selected")}
              </p>
            ) : (
              <>
                {selectedFiles.map((file, index) => (
                  <div
                    key={`file-${file.name}-${index}`}
                    className={file.type?.startsWith("image/")
                      ? "relative overflow-hidden border rounded-lg min-w-0 bg-muted/20"
                      : "flex items-center gap-3 p-3 border rounded-lg min-w-0"}
                  >
                    {file.type?.startsWith("image/") ? (
                      <>
                        <img
                          src={imagePreviews[`${file.name}-${file.size}-${file.lastModified}`]}
                          alt={file.name}
                          className="h-52 w-full object-contain"
                        />
                        <span className="absolute bottom-2 left-2 rounded bg-background/80 px-2 py-1 text-xs text-muted-foreground backdrop-blur-sm">
                          {formatFileSize(file.size)}
                        </span>
                      </>
                    ) : (
                      <File className="h-5 w-5 text-muted-foreground shrink-0" />
                    )}
                    {!file.type?.startsWith("image/") && (
                      <div className="flex-1 min-w-0 max-w-full overflow-hidden">
                        <p className="text-sm font-medium truncate" title={file.name}>{file.name}</p>
                        <p className="text-xs text-muted-foreground">
                          {formatFileSize(file.size)}
                        </p>
                      </div>
                    )}
                    <Button
                      variant="ghost"
                      size="icon"
                      className={file.type?.startsWith("image/")
                        ? "absolute right-2 top-2 h-8 w-8 bg-background/80 backdrop-blur-sm hover:bg-background"
                        : "h-8 w-8 shrink-0"}
                      disabled={isUploading}
                      onClick={() => handleRemoveFile(index)}
                    >
                      <X className="h-4 w-4" />
                    </Button>
                  </div>
                ))}
                {selectedFilePaths.map((filePath, index) => (
                  <div
                    key={`path-${filePath}-${index}`}
                    className="flex items-center gap-3 p-3 border rounded-lg min-w-0"
                  >
                    <File className="h-5 w-5 text-muted-foreground shrink-0" />
                    <div className="flex-1 min-w-0 max-w-full overflow-hidden">
                      <p className="text-sm font-medium truncate" title={filePath.split(/[/\\]/).pop() || filePath}>{filePath.split(/[/\\]/).pop() || filePath}</p>
                      <p className="text-xs text-muted-foreground truncate" title={filePath}>
                        {filePath}
                      </p>
                    </div>
                    <Button
                      variant="ghost"
                      size="icon"
                      className="h-8 w-8 shrink-0"
                      disabled={isUploading}
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
            disabled={isUploading}
          >
            {t("add_more_files")}
          </Button>
          <input
            ref={fileInputRef}
            type="file"
            multiple
            className="hidden"
            disabled={isUploading}
            onChange={handleFileInputChange}
          />
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={handleCancel} disabled={isUploading}>
            {t("cancel")}
          </Button>
          <Button onClick={handleConfirm} disabled={isUploading || (selectedFiles.length === 0 && selectedFilePaths.length === 0)}>
            {isUploading ? (
              <>
                <Loader2 className="mr-2 h-4 w-4 animate-spin" />
                {t("uploading")}
              </>
            ) : (
              t("upload")
            )}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
