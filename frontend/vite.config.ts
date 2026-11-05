import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import wails from "@wailsio/runtime/plugins/vite";

// Wails serves the built frontend over its own wails:// scheme rather than
// http(s). Vite injects crossorigin on <script>/<link>, which makes the webview
// issue a CORS preflight the asset handler never answers — the script then
// silently fails and the window stays blank. Strip the attribute.
function stripCrossorigin() {
  return {
    name: "strip-crossorigin",
    enforce: "post" as const,
    transformIndexHtml(html: string) {
      return html.replace(/\s+crossorigin(?:="[^"]*")?/g, "");
    },
  };
}

// https://vitejs.dev/config/
export default defineConfig({
  server: {
    host: "127.0.0.1",
    port: Number(process.env.WAILS_VITE_PORT) || 9245,
    strictPort: true,
  },
  plugins: [react(), wails("./bindings"), stripCrossorigin()],
});
