import { useEffect, useRef } from "react";

/**
 * Hook to track and log component re-renders for performance debugging.
 * Only logs in development mode.
 */
export function useRenderCount(componentName: string, props?: Record<string, unknown>) {
  const renderCount = useRef(0);
  const prevProps = useRef<Record<string, unknown> | undefined>(props);

  useEffect(() => {
    renderCount.current += 1;

    if (import.meta.env.DEV) {
      // Check which props changed
      const changedProps: string[] = [];
      if (props && prevProps.current) {
        Object.keys(props).forEach((key) => {
          if (props[key] !== prevProps.current![key]) {
            changedProps.push(key);
          }
        });
      }

      if (renderCount.current > 1) {
        console.log(
          `[Render #${renderCount.current}] ${componentName}${
            changedProps.length > 0 ? ` - Props changed: ${changedProps.join(", ")}` : ""
          }`
        );
      } else {
        console.log(`[Render #1] ${componentName} - Initial render`);
      }
    }

    prevProps.current = props;
  });
}
