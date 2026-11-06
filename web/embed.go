// Package web holds the browser UI for AI Agent World, embedded into the
// binary so the service ships as a single file with no asset directory to
// deploy or to get out of step with the code.
package web

import "embed"

// FS is the UI tree: index.html, app.js, styles.css, assets/ and fonts/.
//
// The fonts are embedded rather than loaded from Google's CDN so the page
// keeps working on a machine with no route to the internet, and so that
// opening it does not tell a third party who is using the service.
//
//go:embed all:assets all:fonts index.html app.js styles.css manifest.webmanifest
var FS embed.FS
