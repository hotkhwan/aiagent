package world

import "testing"

// TestPhonesMayHandOverTokensAndSignBrowsersIn: the app's account screen and
// its QR login run on a phone token, so the device allowlist must let them
// through; both are tied to the phone (a pairing code yields that user
// token's device, a browser approved by a phone is its child). Machine
// management stays closed.
func TestPhonesMayHandOverTokensAndSignBrowsersIn(t *testing.T) {
	may := func(method, path string) bool {
		for _, re := range deviceRouteRE[method] {
			if re.MatchString(path) {
				return true
			}
		}
		return false
	}
	for _, p := range []string{"/tokens/0123456789ab/pair", "/login-qr/AAAAAAAAAAAAAAAAAAAAAA/info", "/login-qr/AAAAAAAAAAAAAAAAAAAAAA/approve"} {
		if !may("POST", p) {
			t.Errorf("a phone cannot POST %s", p)
		}
	}
	for _, p := range []string{"/vault", "/update", "/connect/qr", "/environments", "/login-qr/short/approve", "/tokens/0123456789ab/pair/x"} {
		if may("POST", p) {
			t.Errorf("a phone may POST %s", p)
		}
	}
}
