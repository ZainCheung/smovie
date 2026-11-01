import { defineConfig } from "mygo-cli";

// Native UI app: no frontend to develop or build. The player's <video>
// page is served by the Go app itself (server.go).
export default defineConfig({
  name: "smovie",
  identifier: "com.example.smovie",
  version: "0.1.0",
  out: "build",
});
