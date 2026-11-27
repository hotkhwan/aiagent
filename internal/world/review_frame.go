package world

import (
	"bytes"
	"regexp"
)

// reviewFrameScript adds a verdict bar to every section of a review page (the
// elements marked data-item, else each h2) and reports the verdicts to the
// window that framed it. The frame is sandboxed without same-origin, so the
// parent can only hear it through postMessage.
const reviewFrameScript = `(function () {
  var marked = Array.prototype.slice.call(document.querySelectorAll('[data-item]'));
  var nodes = marked.length ? marked : Array.prototype.slice.call(document.querySelectorAll('h2'));
  var state = nodes.map(function (el, i) {
    var label = marked.length ? (el.getAttribute('data-item') || '') : '';
    var heading = el.tagName === 'H2' ? el : el.querySelector('h2, h3');
    var title = (label || (heading ? heading.textContent : '') || ('ข้อ ' + (i + 1))).replace(/\s+/g, ' ').trim();
    return { el: el, title: title, verdict: '', note: '' };
  });
  function report() {
    window.parent.postMessage({ aiagentReview: true, items: state.map(function (s) {
      return { title: s.title, verdict: s.verdict, note: s.note };
    }) }, '*');
  }
  function button(label, bg, fg) {
    var b = document.createElement('button');
    b.type = 'button';
    b.textContent = label;
    b.setAttribute('style', 'padding:8px 16px;min-height:44px;border:0;border-radius:999px;background:' + bg + ';color:' + fg + ';font:700 15px system-ui,sans-serif;cursor:pointer');
    return b;
  }
  state.forEach(function (s) {
    var bar = document.createElement('div');
    bar.setAttribute('style', 'margin:12px 0;padding:10px 12px;border-radius:12px;background:#FFF4DC;color:#513622;font:15px/1.4 system-ui,sans-serif;display:flex;flex-wrap:wrap;gap:8px;align-items:center');
    var ok = button('ผ่าน', '#D8F5E3', '#1B6B43');
    var fix = button('ต้องแก้ไข', '#FFE0B3', '#8A4B00');
    var area = document.createElement('textarea');
    area.rows = 2;
    area.placeholder = 'ต้องแก้อะไร (บังคับเมื่อเลือกต้องแก้ไข)';
    area.setAttribute('style', 'display:none;flex:1 1 100%;min-height:52px;border-radius:10px;border:1px solid #E2C9A0;padding:8px;font:inherit;color:inherit;background:#fff');
    function paint() {
      ok.style.outline = s.verdict === 'approve' ? '3px solid #1B6B43' : 'none';
      fix.style.outline = s.verdict === 'revise' ? '3px solid #8A4B00' : 'none';
      area.style.display = s.verdict === 'revise' ? 'block' : 'none';
    }
    ok.addEventListener('click', function () { s.verdict = 'approve'; paint(); report(); });
    fix.addEventListener('click', function () { s.verdict = 'revise'; paint(); report(); area.focus(); });
    area.addEventListener('input', function () { s.note = area.value; report(); });
    bar.appendChild(ok);
    bar.appendChild(fix);
    bar.appendChild(area);
    if (marked.length) s.el.insertBefore(bar, s.el.firstChild);
    else s.el.parentNode.insertBefore(bar, s.el.nextSibling);
  });
  report();
})();`

// darkQueryRE finds a page's dark-mode media queries.
var darkQueryRE = regexp.MustCompile(`(?i)prefers-color-scheme\s*:\s*dark`)

// lightOnly keeps a review page in its light design wherever it is shown. A
// browser in dark mode (the web page's own color-scheme: dark, an iPhone in
// dark mode in the app's web view) tells a framed page it is dark, and agents'
// pages often carry half-done dark rules: code chips came out white on white.
// The dark queries are made unknown, so they never match, and the page says
// it is light.
func lightOnly(page []byte) []byte {
	page = darkQueryRE.ReplaceAll(page, []byte("aiagent-light-only"))
	meta := []byte(`<meta name="color-scheme" content="light"><style>:root{color-scheme:light}</style>`)
	lower := bytes.ToLower(page)
	if i := bytes.Index(lower, []byte("<head>")); i >= 0 {
		i += len("<head>")
		return append(append(append([]byte{}, page[:i]...), meta...), page[i:]...)
	}
	return append(meta, page...)
}

// withReviewFrame returns the page, light only, with the verdict script before </body>.
func withReviewFrame(page []byte) []byte {
	page = lightOnly(page)
	tag := []byte("<script>" + reviewFrameScript + "</script>")
	i := bytes.LastIndex(bytes.ToLower(page), []byte("</body>"))
	if i < 0 {
		return append(append([]byte{}, page...), tag...)
	}
	out := make([]byte, 0, len(page)+len(tag))
	out = append(out, page[:i]...)
	out = append(out, tag...)
	return append(out, page[i:]...)
}
