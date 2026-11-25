// docs/guide/pdf.mjs - print out/*.html to ../../web/docs/*.pdf
import { chromium } from 'playwright'
import { fileURLToPath } from 'url'
import path from 'path'
const here = path.dirname(fileURLToPath(import.meta.url)), b = await chromium.launch()
for (const n of ['user-manual', 'admin-manual', 'user-slides', 'admin-slides']) {
  const p = await b.newPage()
  await p.goto('file://' + here + '/out/' + n + '.html'); await p.waitForTimeout(1200)
  await p.pdf(Object.assign({ path: here + '/../../web/docs/' + n + '.pdf', printBackground: true, preferCSSPageSize: true }))
  await p.close()
}
await b.close()
