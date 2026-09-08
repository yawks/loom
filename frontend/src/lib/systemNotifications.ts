import {
  CheckNotificationAuthorization,
  InitializeNotifications,
  IsNotificationAvailable,
  RequestNotificationAuthorization,
} from "../../wailsjs/runtime/runtime";

let initialization: Promise<boolean> | undefined;

export function initializeSystemNotifications(): Promise<boolean> {
  if (!initialization) {
    initialization = InitializeNotifications()
      .then(() => IsNotificationAvailable())
      .catch((error) => {
        console.error("Unable to initialize notifications", error);
        return false;
      });
  }
  return initialization;
}

export async function authorizeSystemNotifications(): Promise<boolean> {
  if (!await initializeSystemNotifications()) return false;
  if (await CheckNotificationAuthorization().catch(() => false)) return true;
  return RequestNotificationAuthorization().catch((error) => {
    console.error("Unable to authorize notifications", error);
    return false;
  });
}
