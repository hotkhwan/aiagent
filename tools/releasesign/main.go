// Command releasesign signs a release's SHA256SUMS with the owner's ed25519
// release key (packaging/release.sh runs it). It never prints the private key.
//
//	releasesign keygen -key FILE           make a new key (PKCS#8 PEM, mode 0600); refuses to overwrite
//	releasesign pubkey -key FILE           print the public key, base64 (for internal/world/release_pubkey.go)
//	releasesign sign -key FILE -in SUMS -out SIG
//	                                       sign SUMS, write the base64 signature, and refuse
//	                                       unless it verifies with the key compiled into aiagent
//	releasesign check -in DIR              verify DIR as every platform's installed aiagent
//	                                       will, before it is published
//
// A downloaded release is checked with `aiagent verify-release DIR`.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/hotkhwan/aiagent/internal/release"
	"github.com/hotkhwan/aiagent/internal/world"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: releasesign keygen|pubkey|sign|verify ...")
		os.Exit(2)
	}
	fs := flag.NewFlagSet(os.Args[1], flag.ExitOnError)
	keyFile := fs.String("key", "", "the private key (PKCS#8 PEM)")
	in := fs.String("in", "", "SHA256SUMS to sign")
	out := fs.String("out", "", "where to write the signature")
	_ = fs.Parse(os.Args[2:])
	var err error
	switch os.Args[1] {
	case "keygen":
		err = keygen(*keyFile)
	case "pubkey":
		var k ed25519.PrivateKey
		if k, err = readKey(*keyFile); err == nil {
			fmt.Println(base64.StdEncoding.EncodeToString(k.Public().(ed25519.PublicKey)))
		}
	case "sign":
		err = sign(*keyFile, *in, *out)
	case "check":
		err = check(*in)
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "releasesign:", err)
		os.Exit(1)
	}
}

func keygen(path string) error {
	if path == "" {
		return errors.New("-key is required")
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if err := pem.Encode(f, &pem.Block{Type: "PRIVATE KEY", Bytes: der}); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func readKey(path string) (ed25519.PrivateKey, error) {
	if path == "" {
		return nil, errors.New("-key is required")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	blk, _ := pem.Decode(b)
	if blk == nil || blk.Type != "PRIVATE KEY" {
		return nil, errors.New("the key file is not a PKCS#8 PEM private key")
	}
	k, err := x509.ParsePKCS8PrivateKey(blk.Bytes)
	if err != nil {
		return nil, errors.New("the key file does not parse")
	}
	ek, ok := k.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("the key is not ed25519")
	}
	return ek, nil
}

// platforms are what packaging/release.sh builds.
var platforms = [][2]string{{"darwin", "arm64"}, {"darwin", "amd64"}, {"linux", "amd64"}, {"linux", "arm64"}}

func check(dir string) error {
	if dir == "" {
		return errors.New("-in DIR is required")
	}
	for _, p := range platforms {
		v, err := release.Verify(dir, p[0], p[1], world.ReleasePublicKey())
		if err != nil {
			return fmt.Errorf("%s/%s: %w", p[0], p[1], err)
		}
		fmt.Printf("ok %s/%s v%s\n", p[0], p[1], v.Version)
	}
	return nil
}

func sign(keyFile, in, out string) error {
	if in == "" || out == "" {
		return errors.New("-in and -out are required")
	}
	k, err := readKey(keyFile)
	if err != nil {
		return err
	}
	sums, err := os.ReadFile(in)
	if err != nil {
		return err
	}
	if _, err := release.ParseSums(sums); err != nil {
		return err
	}
	sig := []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(k, sums)) + "\n")
	// a release only installed machines can verify is the only useful kind
	if err := release.VerifySignature(sums, sig, world.ReleasePublicKey()); err != nil {
		return errors.New("this key is not the one compiled into aiagent (internal/world/release_pubkey.go)")
	}
	return os.WriteFile(out, sig, 0o644)
}
