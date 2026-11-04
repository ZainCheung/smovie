import { defineConfig } from "mygo-cli";
import pkg from "./package.json" with { type: "json" };

// Native UI app: no frontend to develop or build. The player's <video>
// page is served by the Go app itself (server.go).
export default defineConfig({
  name: "smovie",
  identifier: "com.deepoke.smovie",
  // CI checks that a release tag vX.Y.Z matches this.
  version: pkg.version,
  out: "build",
  // Windows .ico (16–256 px) and Linux icons are rendered from this 1024 px
  // PNG. It lives outside resources/, which ships inside the app as is.
  icon: "icon/icon.png",
  // macOS: the Icon Composer document (open it in Icon Composer to edit),
  // compiled with Xcode 26+; without it the build falls back to icon.png.
  macos: {
    icon: "icon/AppIcon.icon",
    // Without these, Gatekeeper refuses the app on other Macs. The keychain
    // is CI's, and on a Mac the login keychain holds the "notary" profile:
    // `xcrun notarytool store-credentials notary`. If a Mac holds two
    // certificates of this name, name one with `bun run build -- -sign <SHA-1>`.
    signingIdentity: "Developer ID Application: yuanyuan yu (W74WTW3479)",
    notarize: { keychainProfile: "notary", keychain: process.env.NOTARY_KEYCHAIN },
  },
});
