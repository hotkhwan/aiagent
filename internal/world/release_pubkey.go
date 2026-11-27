package world

import (
	"crypto/ed25519"
	"encoding/base64"
)

// releasePublicKey verifies the SHA256SUMS of every published aiagent release
// (internal/release). Its private half is the owner's, kept off the repo at
// ~/littledays-keys/aiagent-release-ed25519.pem on the release Mac, and only
// packaging/release.sh uses it.
//
// Made once with
//
//	openssl genpkey -algorithm ed25519 -out ~/littledays-keys/aiagent-release-ed25519.pem
//	chmod 600 ~/littledays-keys/aiagent-release-ed25519.pem
//	openssl pkey -in ~/littledays-keys/aiagent-release-ed25519.pem -pubout -outform DER | tail -c 32 | base64
//
// (or `go run ./tools/releasesign pubkey -key <pem>`). The same key as PEM is
// packaging/release-pubkey.pem, for checking a download by hand.
//
// Replacing it means every installed aiagent refuses releases signed with the
// new key until it is updated some other way: rotate only if the key leaks.
const releasePublicKey = "79kK0JDtBtjsXN7QBFJkwYZ43q8dzM70HJDD54eqpj4="

// ReleasePublicKey is the compiled-in release key.
func ReleasePublicKey() ed25519.PublicKey {
	b, err := base64.StdEncoding.DecodeString(releasePublicKey)
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil
	}
	return ed25519.PublicKey(b)
}
