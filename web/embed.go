// Package web holds the browser UI for AI Agent World, embedded into the
// binary so the service ships as a single file with no asset directory to
// deploy or to get out of step with the code.
package web

import "embed"

// FS is the UI tree: index.html, app.js, styles.css and assets/.
//
//go:embed all:assets index.html app.js styles.css manifest.webmanifest
var FS embed.FS
