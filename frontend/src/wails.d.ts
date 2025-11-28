// Type declarations for Wails runtime
interface Window {
  runtime?: {
    listeners?: {
      [eventName: string]: Array<(...args: any[]) => void>;
    };
  };
}


