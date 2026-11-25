// internal/world/guides.go
//
// The manuals and slide decks, as PDFs built from docs/guide and embedded with
// the page. The admin's are for the admin token only.
package world

import (
	"io/fs"
	"regexp"

	"github.com/gofiber/fiber/v3"
)

var guideRE = regexp.MustCompile(`^(user|admin)-(manual|slides)$`)

func (s *Service) handleGuide(c fiber.Ctx) error {
	m := guideRE.FindStringSubmatch(c.Params("name"))
	if m == nil || (m[1] == "admin" && identity(c).Role != RoleAdmin) {
		return hidden(c)
	}
	b, err := fs.ReadFile(uiFS, "docs/"+m[0]+".pdf")
	if err != nil {
		return hidden(c)
	}
	c.Set("Content-Type", "application/pdf")
	c.Set("Content-Disposition", `attachment; filename="aiagent-`+m[0]+`.pdf"`)
	c.Set("Cache-Control", "no-store")
	return c.Send(b)
}
