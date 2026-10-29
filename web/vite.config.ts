import { writeFileSync } from "node:fs"
import { resolve } from "node:path"
import babel from "@rolldown/plugin-babel"
import tailwindcss from "@tailwindcss/vite"
import react, { reactCompilerPreset } from "@vitejs/plugin-react"
import { defineConfig, type Plugin } from "vite"

// dist is emptied on every build, but web.go embeds it and must compile
// before the app is built, so the build puts back the tracked .gitignore.
function keepDistTracked(): Plugin {
  return {
    name: "keep-dist-tracked",
    apply: "build",
    closeBundle() {
      writeFileSync(
        resolve(import.meta.dirname, "dist/.gitignore"),
        "# The dashboard build lands here and is embedded by web.go; only this file is tracked.\n*\n!.gitignore\n"
      )
    },
  }
}

export default defineConfig({
  plugins: [react(), babel({ presets: [reactCompilerPreset()] }), tailwindcss(), keepDistTracked()],
  resolve: {
    alias: {
      "@": resolve(import.meta.dirname, "./src"),
    },
  },
  build: {
    // React, the router and Radix make up the main chunk; the service sends it
    // gzipped and browsers cache the hashed assets for good. CodeMirror loads
    // separately, when a body is first shown.
    chunkSizeWarningLimit: 800,
  },
  server: {
    // The development server forwards API calls to the local service.
    proxy: Object.fromEntries(
      ["/v1", "/ca.crt", "/ca.pem"].map((path) => [
        path,
        process.env.MIHOMO_CAPTURE_API ?? "http://127.0.0.1:19090",
      ])
    ),
  },
})
