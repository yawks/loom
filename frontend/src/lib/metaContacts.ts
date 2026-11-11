import { GetMetaContactChanges, GetMetaContacts } from "../../wailsjs/go/main/App";
import { createCompatibleContactLoader } from "./contactChanges";

// Generic API feature detection for development with an older backend.
export const fetchMetaContacts = createCompatibleContactLoader(
  GetMetaContactChanges,
  GetMetaContacts,
  () => typeof window.go?.main?.App?.GetMetaContactChanges === "function",
);
