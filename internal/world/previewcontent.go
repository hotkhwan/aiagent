// internal/world/previewcontent.go
//
// The content half of the preview: what a room's instance or its dev server
// answers, with nothing in it that could speak for the origin. Response
// headers are an allowlist, so what a room sends (Set-Cookie,
// Service-Worker-Allowed, Clear-Site-Data, its own CSP, ...) never reaches the
// browser, and every answer, errors included, carries the sandbox.
package world

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	previewCSP        = "sandbox allow-scripts allow-forms"
	previewMaxHTML    = 8 << 20
	previewMaxRequest = 32 << 20
)

// contentHead replaces every header with the fixed ones.
func contentHead(h http.Header) {
	for k := range h {
		h.Del(k)
	}
	h.Set("Content-Security-Policy", previewCSP)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cache-Control", "no-store")
	// a sandboxed document has the origin "null"; without this its module
	// scripts and fonts do not load. No credentials are ever allowed.
	h.Set("Access-Control-Allow-Origin", "null")
}

func contentError(w http.ResponseWriter, status int) {
	contentHead(w.Header())
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, http.StatusText(status)+"\n")
}

// fromTrustedPage reports a request another page of this origin made for a
// sub-resource. A room's file must not become such a page's script or worker;
// only framing it, or opening it, is fine, and both are sandboxed.
func fromTrustedPage(r *http.Request) bool {
	if strings.EqualFold(r.Header.Get("Service-Worker"), "script") {
		return true
	}
	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-origin", "same-site":
		switch r.Header.Get("Sec-Fetch-Dest") {
		case "iframe", "frame", "document":
			return false
		}
		return true
	}
	return false
}

// content answers one request below base ("/dev/<room>/~<capability>/"), which
// the caller has checked.
func (p *Preview) content(w http.ResponseWriter, r *http.Request, room, base string) {
	if fromTrustedPage(r) {
		contentError(w, http.StatusForbidden)
		return
	}
	if r.URL.EscapedPath()+"/" == base {
		contentHead(w.Header())
		w.Header().Set("Location", base)
		w.WriteHeader(http.StatusFound)
		return
	}
	if !strings.HasPrefix(r.URL.EscapedPath(), base) || !strings.HasPrefix(r.URL.Path, base) {
		contentError(w, http.StatusNotFound)
		return
	}
	if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
		contentHead(w.Header())
		w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, POST, PUT, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "*")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	p.mu.Lock()
	full := p.streams[room] >= previewMaxStreams
	if !full {
		p.streams[room]++
	}
	p.mu.Unlock()
	if full {
		contentError(w, http.StatusTooManyRequests)
		return
	}
	defer func() {
		p.mu.Lock()
		if p.streams[room]--; p.streams[room] <= 0 {
			delete(p.streams, room)
		}
		p.mu.Unlock()
	}()
	st, err := p.state(r.Context(), room)
	if err != nil {
		contentError(w, http.StatusBadGateway)
		return
	}
	switch st.Kind {
	case "static":
		p.static(w, r, room, base)
	case "port":
		p.devServer(w, r, room, base, st.Port)
	default:
		contentError(w, http.StatusNotFound)
	}
}

// static asks the room's instance for a file: it reads it as the room's user.
func (p *Preview) static(w http.ResponseWriter, r *http.Request, room, base string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		contentError(w, http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, base)
	status, hdr, b, err := p.svc.OSUsers.do(r.Context(), room, "GET", apiPrefix+"/preview/file?path="+url.QueryEscape(name), "", nil)
	if err != nil {
		contentError(w, http.StatusBadGateway)
		return
	}
	if status != http.StatusOK {
		if status != http.StatusRequestEntityTooLarge {
			status = http.StatusNotFound
		}
		contentError(w, status)
		return
	}
	// a folder was asked for without its slash
	if hdr.Get("X-Preview-Index") == "1" && name != "" && !strings.HasSuffix(name, "/") {
		contentHead(w.Header())
		w.Header().Set("Location", base+(&url.URL{Path: name}).EscapedPath()+"/")
		w.WriteHeader(http.StatusFound)
		return
	}
	ct := mime.TypeByExtension(path.Ext(name))
	switch strings.ToLower(path.Ext(name)) {
	case "", ".html", ".htm":
		ct = "text/html; charset=utf-8"
	case ".js", ".mjs":
		ct = "text/javascript; charset=utf-8"
	case ".wasm":
		ct = "application/wasm"
	}
	if hdr.Get("X-Preview-Index") != "1" && path.Ext(name) == "" {
		ct = "text/plain; charset=utf-8"
	}
	if ct == "" {
		ct = "application/octet-stream"
	}
	if strings.HasPrefix(ct, "text/html") {
		b = injectPreview(b, base)
	}
	contentHead(w.Header())
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Length", strconv.Itoa(len(b)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(b)
	}
}

// ---------- dev server ----------

var devResponseHeaders = []string{"Content-Type", "Content-Language", "Content-Encoding", "Content-Length", "Content-Range", "Accept-Ranges", "Etag", "Last-Modified", "Sourcemap", "X-Sourcemap"}

// devRequestHeaders is what a request may carry to a room: never the origin's
// cookies, a credential, the address of the visitor or this service's own
// headers.
func devRequestHeaders(in http.Header, port int) http.Header {
	out := http.Header{}
	for k, v := range in {
		l := strings.ToLower(k)
		switch l {
		case "cookie", "cookie2", "authorization", "proxy-authorization", "proxy-connection", "connection", "keep-alive", "te", "trailer", "transfer-encoding", "upgrade",
			"referer", "forwarded", "via", "true-client-ip", "cdn-loop", "accept-encoding", "origin", "expect", "host", "if-none-match", "if-modified-since":
			continue
		}
		if strings.HasPrefix(l, "x-forwarded-") || strings.HasPrefix(l, "x-real-") || strings.HasPrefix(l, "x-envoy-") || strings.HasPrefix(l, "x-request-") ||
			strings.HasPrefix(l, "cf-") || strings.HasPrefix(l, "x-aiagent-") || strings.HasPrefix(l, "x-auth-") || strings.HasPrefix(l, "x-b3-") || strings.HasPrefix(l, "sec-fetch-") ||
			l == "x-client-ip" || l == "x-original-forwarded-for" || l == "traceparent" || l == "tracestate" {
			continue
		}
		out[k] = v
	}
	if in.Get("Origin") != "" {
		out.Set("Origin", "http://localhost:"+strconv.Itoa(port))
	}
	return out
}

// devServer proxies to a server the room's agent started.
func (p *Preview) devServer(w http.ResponseWriter, r *http.Request, room, base string, port int) {
	rest := "/" + strings.TrimPrefix(r.URL.EscapedPath(), base)
	target := "http://localhost:" + strconv.Itoa(port) + rest
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	var body io.Reader
	if r.ContentLength != 0 {
		body = http.MaxBytesReader(w, r.Body, previewMaxRequest)
	}
	out, err := http.NewRequestWithContext(r.Context(), r.Method, target, body)
	if err != nil {
		contentError(w, http.StatusBadRequest)
		return
	}
	out.Header = devRequestHeaders(r.Header, port)
	out.ContentLength = r.ContentLength
	if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		p.devSocket(w, r, out, room, port)
		return
	}
	tr := &http.Transport{
		DisableKeepAlives: true, DisableCompression: true, ResponseHeaderTimeout: 60 * time.Second, MaxResponseHeaderBytes: 64 << 10,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return p.dialRoom(ctx, room, port) },
	}
	defer tr.CloseIdleConnections()
	resp, err := tr.RoundTrip(out)
	if err != nil {
		contentError(w, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusProxyAuthRequired {
		// 101 was not asked for; 401 would raise the browser's own sign-in
		// box for this origin
		contentError(w, http.StatusBadGateway)
		return
	}
	loc := resp.Header.Get("Location")
	if loc != "" {
		if loc = devLocation(loc, base, port); loc == "" {
			contentError(w, http.StatusBadGateway)
			return
		}
	}
	contentHead(w.Header())
	for _, k := range devResponseHeaders {
		if v := resp.Header.Get(k); v != "" {
			w.Header().Set(k, v)
		}
	}
	if loc != "" {
		w.Header().Set("Location", loc)
	}
	ct, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if ct == "multipart/x-mixed-replace" { // each part brings its own headers
		contentError(w, http.StatusBadGateway)
		return
	}
	if ct == "text/html" && resp.Header.Get("Content-Encoding") == "" && r.Method != http.MethodHead {
		b, err := io.ReadAll(io.LimitReader(resp.Body, previewMaxHTML+1))
		if err != nil || len(b) > previewMaxHTML {
			contentError(w, http.StatusBadGateway)
			return
		}
		b = injectPreview(b, base)
		w.Header().Set("Content-Length", strconv.Itoa(len(b)))
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(b)
		return
	}
	w.WriteHeader(resp.StatusCode)
	rc := http.NewResponseController(w)
	buf := make([]byte, 32<<10)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			_ = rc.Flush()
		}
		if err != nil {
			return
		}
	}
}

// devLocation keeps a redirect inside the room's page; anywhere else is "".
func devLocation(loc, base string, port int) string {
	u, err := url.Parse(loc)
	if err != nil || strings.ContainsAny(loc, "\\\r\n") {
		return ""
	}
	if u.Scheme != "" || u.Host != "" {
		if u.Scheme != "http" && u.Scheme != "" || u.Port() != strconv.Itoa(port) || u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1" {
			return ""
		}
	}
	clean := u.EscapedPath()
	for _, seg := range strings.Split(strings.ToLower(clean), "/") {
		if seg == ".." || strings.Contains(seg, "%2e") {
			return ""
		}
	}
	if u.Host == "" && !strings.HasPrefix(u.Path, "/") {
		return u.String()
	}
	out := base + strings.TrimPrefix(clean, "/")
	if u.RawQuery != "" {
		out += "?" + u.RawQuery
	}
	if u.Fragment != "" {
		out += "#" + u.EscapedFragment()
	}
	return out
}

// devSocket carries a WebSocket (hot reload) to the room's dev server.
func (p *Preview) devSocket(w http.ResponseWriter, r *http.Request, out *http.Request, room string, port int) {
	if r.Method != http.MethodGet {
		contentError(w, http.StatusBadRequest)
		return
	}
	conn, err := p.dialRoom(r.Context(), room, port)
	if err != nil {
		contentError(w, http.StatusBadGateway)
		return
	}
	defer conn.Close()
	out.Header.Set("Connection", "Upgrade")
	out.Header.Set("Upgrade", "websocket")
	_ = conn.SetDeadline(time.Now().Add(20 * time.Second))
	if err := out.Write(conn); err != nil {
		contentError(w, http.StatusBadGateway)
		return
	}
	// headers are bounded; what follows them is the socket's own traffic
	lim := &io.LimitedReader{R: conn, N: 64 << 10}
	br := bufio.NewReaderSize(lim, 4096)
	resp, err := http.ReadResponse(br, out)
	if err != nil || resp.StatusCode != http.StatusSwitchingProtocols || !strings.EqualFold(resp.Header.Get("Upgrade"), "websocket") {
		contentError(w, http.StatusBadGateway)
		return
	}
	lim.N = 1 << 62
	_ = conn.SetDeadline(time.Time{})
	client, brw, err := http.NewResponseController(w).Hijack()
	if err != nil {
		contentError(w, http.StatusInternalServerError)
		return
	}
	defer client.Close()
	_ = client.SetDeadline(time.Time{})
	h := http.Header{}
	contentHead(h)
	h.Set("Connection", "Upgrade")
	h.Set("Upgrade", "websocket")
	for _, k := range []string{"Sec-Websocket-Accept", "Sec-Websocket-Protocol", "Sec-Websocket-Extensions"} {
		if v := resp.Header.Get(k); v != "" {
			h.Set(k, v)
		}
	}
	var head bytes.Buffer
	head.WriteString("HTTP/1.1 101 Switching Protocols\r\n")
	_ = h.Write(&head)
	head.WriteString("\r\n")
	if _, err := client.Write(head.Bytes()); err != nil {
		return
	}
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(conn, brw.Reader); done <- struct{}{} }()
	go func() { _, _ = io.Copy(client, br); done <- struct{}{} }()
	select {
	case <-done:
	case <-r.Context().Done():
	}
}

// ---------- what is added to a page ----------

var (
	headTagRE = regexp.MustCompile(`(?i)<head(\s[^>]*)?>`)
	doctypeRE = regexp.MustCompile(`(?i)^\s*<!doctype[^>]*>`)
	rootURLRE = regexp.MustCompile(`(?i)(\s(?:src|href|poster|action)\s*=\s*["'])/([^/"'][^"']*)?(["'])`)
)

// injectPreview makes a page work below base and reload when told to. It is a
// convenience for the page, not a boundary: the page could drop it.
//
// An address starting with "/" would leave the room's page, so it is moved
// below base: in the tags here, for module imports by an import map, and for
// calls made by scripts by the small script below. localStorage and cookies
// are not available in a sandboxed document, and that is on purpose.
func injectPreview(page []byte, base string) []byte {
	page = rootURLRE.ReplaceAllFunc(page, func(m []byte) []byte {
		g := rootURLRE.FindSubmatch(m)
		if strings.HasPrefix("/"+string(g[2]), base) {
			return m
		}
		return []byte(string(g[1]) + base + string(g[2]) + string(g[3]))
	})
	bj, _ := json.Marshal(base)
	im, _ := json.Marshal(map[string]any{"imports": map[string]string{"/": base, base: base}})
	add := fmt.Sprintf(`<script>(function(b){`+
		`function f(u){try{var x=new URL(u,document.baseURI);if(x.host===location.host&&x.pathname.indexOf(b)!==0){x.pathname=b+x.pathname.slice(1);return x.href}}catch(e){}return u}`+
		`var F=window.fetch;window.fetch=function(u,o){return F.call(this,typeof u==='string'||u instanceof URL?f(u):u,o)};`+
		`var X=XMLHttpRequest.prototype.open;XMLHttpRequest.prototype.open=function(){arguments[1]=f(arguments[1]);return X.apply(this,arguments)};`+
		`['WebSocket','EventSource'].forEach(function(k){var C=window[k];if(C)window[k]=class extends C{constructor(u,o){o===undefined?super(f(u)):super(f(u),o)}}});`+
		`addEventListener('message',function(e){if(e.source===parent&&parent!==window&&e.data==='aiagentPreviewReload')location.reload()});`+
		`if(parent!==window)parent.postMessage('aiagentPreviewReady','*')`+
		`})(%s)</script><script type="importmap">%s</script>`, bj, im)
	at := 0
	if m := headTagRE.FindIndex(page); m != nil {
		at = m[1]
	} else if m := doctypeRE.FindIndex(page); m != nil {
		at = m[1]
	}
	out := make([]byte, 0, len(page)+len(add))
	out = append(out, page[:at]...)
	out = append(out, add...)
	return append(out, page[at:]...)
}
