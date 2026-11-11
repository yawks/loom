import type { models } from "../../wailsjs/go/models";

export type ContactChanges = {
  revision: string;
  reset: boolean;
  contacts?: models.MetaContact[] | null;
  removedIds?: number[] | null;
};

// Generic compatibility with an older backend that lacks the revisioned API.
export function createCompatibleContactLoader(
  load: (revision: string) => Promise<ContactChanges>,
  fallback: () => Promise<models.MetaContact[]>,
  available: () => boolean,
) {
  const incremental = createContactLoader(load);
  return () => available() ? incremental() : fallback();
}

// One loader is shared by the list and notification navigation. A failed
// request never advances the revision; concurrent readers share the request.
export function createContactLoader(load: (revision: string) => Promise<ContactChanges>) {
  let revision = "";
  let contacts: models.MetaContact[] = [];
  let pending: Promise<models.MetaContact[]> | undefined;
  return () => {
    if (pending) return pending;
    pending = (async () => {
      const update = await load(revision);
      if (!update.revision || (!revision && !update.reset)) throw new Error("Invalid contact snapshot revision");
      if (update.reset) {
        contacts = [...(update.contacts ?? [])];
      } else if (update.contacts?.length || update.removedIds?.length) {
        const next = new Map(contacts.map((contact) => [contact.id, contact]));
        for (const id of update.removedIds ?? []) next.delete(id);
        for (const contact of update.contacts ?? []) next.set(contact.id, contact);
        contacts = [...next.values()].sort((a, b) => a.id - b.id);
      }
      revision = update.revision;
      return contacts;
    })().finally(() => { pending = undefined; });
    return pending;
  };
}
