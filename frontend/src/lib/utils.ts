import { clsx, type ClassValue } from "clsx"
import { twMerge } from "tailwind-merge"

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}

/**
 * Converts a Wails Time type to a JavaScript Date object.
 * The Time type from Wails is actually a string in TypeScript.
 */
export function timeToDate(time: any): Date {
  if (!time) {
    return new Date();
  }
  if (time instanceof Date) {
    return time;
  }
  if (typeof time === 'string') {
    return new Date(time);
  }
  // Fallback: convert to string first (handles Time type from Wails)
  return new Date(String(time));
}
