export type OfficeFormat = "docx" | "pptx" | "xlsx";

export function getOfficeFormat(fileName: string, mimeType: string): OfficeFormat | null {
  const extension = fileName?.split(".").pop()?.toLowerCase();
  if (extension === "docx" || extension === "pptx" || extension === "xlsx") return extension;
  const mime = mimeType?.split(";")[0].trim().toLowerCase();
  switch (mime) {
    case "application/vnd.openxmlformats-officedocument.wordprocessingml.document": return "docx";
    case "application/vnd.openxmlformats-officedocument.presentationml.presentation": return "pptx";
    case "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet": return "xlsx";
    default: return null;
  }
}
