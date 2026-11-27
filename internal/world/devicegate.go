// internal/world/devicegate.go
//
// What a paired phone's token (Identity.DeviceID != "") may call. A phone is
// the device most likely to be lost, so it gets the everyday routes only:
// watching and talking to agents, reviews, faces, its own push and devices.
// Managing the machine (vault, updates, CLIs, accounts, policy, rooms,
// environments) stays on the web with the admin's own token, so a stolen phone
// cannot leave anything behind that outlives its revoke. A new route is closed
// to phones until it is added here, as userRoutes closes it to users.
//
// An admin's phone may also manage user tokens, because every token it makes
// or reissues is tied to it (UserToken.CreatedByDevice) and ends when the
// phone is revoked (Store.RevokeDevice). A user's phone is held to userRoutes
// as well: the gate checks both.
package world

import (
	"regexp"
	"strings"

	"github.com/gofiber/fiber/v3"
)

var deviceRoutes = map[string][]string{
	"GET": {
		`/me`, `/snapshot`, `/models`, `/reviews`, `/guides/user-(manual|slides)`,
		`/sessions/[A-Za-z0-9-]+`, `/sessions/[A-Za-z0-9-]+/(messages|suggest)`,
		`/uploads/[A-Za-z0-9.-]+`, `/reviews/[A-Za-z0-9._-]+/code`,
		`/faces`, `/faces/account`, `/faces/sessions/[A-Za-z0-9-]+`,
		`/connect/devices`, `/tokens`,
	},
	"PUT":    {`/faces/(account|settings)`, `/faces/sessions/[A-Za-z0-9-]+`},
	"DELETE": {`/faces/account`, `/faces/sessions/[A-Za-z0-9-]+`},
	"POST": {
		`/messages`, `/uploads`, `/preview/ticket`, `/push/devices`, `/push/devices/remove`,
		`/connect/pair/cancel`, `/connect/devices/[0-9a-f]{12}/revoke`,
		`/sessions/[A-Za-z0-9-]+/(message|stop|name|look|answer|model|resume|pending/cancel)`,
		`/reviews/[A-Za-z0-9._-]+/(view|compose|decision)`,
		// tied to the phone: they end with it (devices.go RevokeDevice)
		`/tokens`, `/tokens/[0-9a-f]{12}/(revoke|reissue|duty)`, `/tokens/self/duty`,
		// a pairing code for a user token the phone just made or reissued:
		// the device it pairs is that token's, which ends with the phone
		`/tokens/[0-9a-f]{12}/pair`,
		// signing a browser in: a phone's approval ties the browser to the
		// phone (loginqr.go parentId), so revoking the phone ends it too
		`/login-qr/[A-Za-z0-9_-]{22}/(info|approve)`,
	},
}

var deviceRouteRE = func() map[string][]*regexp.Regexp {
	out := map[string][]*regexp.Regexp{}
	for m, ps := range deviceRoutes {
		out[m] = compileAll(ps...)
	}
	return out
}()

// deviceMay decides a phone token's request before any handler runs.
func deviceMay(c fiber.Ctx) bool {
	path, ok := strings.CutPrefix(c.Path(), apiPrefix)
	if !ok {
		return false
	}
	for _, re := range deviceRouteRE[c.Method()] {
		if re.MatchString(path) {
			return true
		}
	}
	return false
}
