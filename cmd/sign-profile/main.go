// Command sign-profile generates Ed25519 key pairs and signs or verifies
// declarative protocol-profile.json hotfix bundles.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"flag"
	"fmt"
	"os"
	"strings"

	"smovie/internal/hongguo"
)

func main() {
	keygen := flag.Bool("keygen", false, "generate a new Ed25519 key pair (base64)")
	sign := flag.Bool("sign", false, "sign -in with -key (or PROTOCOL_SIGNING_PRIVATE_KEY) and write -out")
	verify := flag.Bool("verify", false, "verify -in and -sig against -pub")
	inPath := flag.String("in", "internal/hongguo/profile_default.json", "input protocol profile JSON path")
	outPath := flag.String("out", "", "signature output path (defaults to <in>.sig)")
	sigPath := flag.String("sig", "", "signature path for -verify (defaults to <in>.sig)")
	privKey := flag.String("key", "", "base64 Ed25519 private key (or set PROTOCOL_SIGNING_PRIVATE_KEY)")
	pubKey := flag.String("pub", hongguo.DefaultProtocolPublicKey, "base64 Ed25519 public key for -verify")
	flag.Parse()

	switch {
	case *keygen:
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			fatal(err)
		}
		fmt.Println("PUBLIC_KEY :", base64.StdEncoding.EncodeToString(pub))
		fmt.Println("PRIVATE_KEY:", base64.StdEncoding.EncodeToString(priv))
	case *sign:
		raw, err := os.ReadFile(*inPath)
		if err != nil {
			fatal(err)
		}
		if _, err := hongguo.ValidateProfileJSON(raw, ""); err != nil {
			fatal(fmt.Errorf("profile validation failed: %w", err))
		}
		encoded := strings.TrimSpace(*privKey)
		if encoded == "" {
			encoded = strings.TrimSpace(os.Getenv("PROTOCOL_SIGNING_PRIVATE_KEY"))
		}
		b, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil || len(b) != ed25519.PrivateKeySize {
			fatal(fmt.Errorf("invalid Ed25519 private key (expect 64-byte base64)"))
		}
		sig := ed25519.Sign(ed25519.PrivateKey(b), raw)
		dst := *outPath
		if dst == "" {
			dst = *inPath + ".sig"
		}
		if err := os.WriteFile(dst, []byte(base64.StdEncoding.EncodeToString(sig)+"\n"), 0o644); err != nil {
			fatal(err)
		}
		fmt.Println("signed:", dst)
	case *verify:
		raw, err := os.ReadFile(*inPath)
		if err != nil {
			fatal(err)
		}
		if _, err := hongguo.ValidateProfileJSON(raw, ""); err != nil {
			fatal(fmt.Errorf("profile validation failed: %w", err))
		}
		sp := *sigPath
		if sp == "" {
			sp = *inPath + ".sig"
		}
		sigRaw, err := os.ReadFile(sp)
		if err != nil {
			fatal(err)
		}
		pub, err := base64.StdEncoding.DecodeString(strings.TrimSpace(*pubKey))
		if err != nil || len(pub) != ed25519.PublicKeySize {
			fatal(fmt.Errorf("invalid Ed25519 public key"))
		}
		sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sigRaw)))
		if err != nil || len(sig) != ed25519.SignatureSize {
			fatal(fmt.Errorf("invalid Ed25519 signature"))
		}
		if !ed25519.Verify(ed25519.PublicKey(pub), raw, sig) {
			fatal(fmt.Errorf("signature verification failed"))
		}
		fmt.Println("verified:", *inPath)
	default:
		flag.Usage()
		os.Exit(2)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "sign-profile:", err)
	os.Exit(1)
}
