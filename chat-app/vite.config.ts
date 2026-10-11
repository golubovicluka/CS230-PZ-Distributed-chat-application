import path from "path"
import tailwindcss from "@tailwindcss/vite"
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react-swc'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src"),
    },
  },
  server: {
    proxy: {
      "/lb": {
        target: process.env.LB_URL ?? "http://127.0.0.1:9000",
        rewrite: (requestPath) => requestPath.replace(/^\/lb/, ""),
      },
      "/api": {
        target: process.env.CHAT_API_URL ?? "http://127.0.0.1:8080",
        rewrite: (requestPath) => requestPath.replace(/^\/api/, ""),
      },
    },
  },
})
