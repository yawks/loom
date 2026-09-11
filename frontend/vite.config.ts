import path from "path"
import react from "@vitejs/plugin-react"
import { defineConfig } from "vite"

export default defineConfig({
  // Vite 7 must preserve the parser WASM asset URLs during development.
  optimizeDeps: { exclude: ["@silurus/ooxml"] },
  plugins: [react()],
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src"),
    },
  },
})
