package mailmime

import (
	"encoding/base64"
	"html"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/microcosm-cc/bluemonday"
	nethtml "golang.org/x/net/html"
)

// InlineImage is the data of a cid: referenced part.
type InlineImage struct {
	ContentID string
	MimeType  string
	Data      []byte
}

var cidRe = regexp.MustCompile(`(?i)cid:([^"'\s)>]+)`)

// RewriteCID replaces cid: references with data: URIs.
func RewriteCID(body string, images []InlineImage) string {
	if len(images) == 0 || !strings.Contains(strings.ToLower(body), "cid:") {
		return body
	}
	byID := map[string]InlineImage{}
	for _, im := range images {
		byID[strings.ToLower(strings.Trim(im.ContentID, "<>"))] = im
	}
	return cidRe.ReplaceAllStringFunc(body, func(m string) string {
		id := strings.ToLower(m[4:])
		im, ok := byID[id]
		if !ok {
			// Some clients URL-encode the content id.
			im, ok = byID[strings.ReplaceAll(id, "%40", "@")]
		}
		if !ok || !strings.HasPrefix(strings.ToLower(im.MimeType), "image/") {
			return m
		}
		return "data:" + strings.ToLower(im.MimeType) + ";base64," + base64.StdEncoding.EncodeToString(im.Data)
	})
}

var (
	policyOnce sync.Once
	policy     *bluemonday.Policy
)

var styleProps = []string{
	"color", "background", "background-color", "background-image", "background-repeat", "background-position", "background-size",
	"border", "border-top", "border-right", "border-bottom", "border-left", "border-color", "border-style", "border-width",
	"border-top-color", "border-right-color", "border-bottom-color", "border-left-color",
	"border-top-style", "border-right-style", "border-bottom-style", "border-left-style",
	"border-top-width", "border-right-width", "border-bottom-width", "border-left-width",
	"border-collapse", "border-spacing", "border-radius", "box-shadow", "box-sizing",
	"display", "float", "clear", "visibility", "opacity", "overflow", "overflow-x", "overflow-y",
	"font", "font-family", "font-size", "font-style", "font-weight", "font-variant", "letter-spacing", "line-height",
	"height", "width", "max-width", "min-width", "max-height", "min-height",
	"list-style", "list-style-type", "list-style-position",
	"margin", "margin-top", "margin-right", "margin-bottom", "margin-left",
	"padding", "padding-top", "padding-right", "padding-bottom", "padding-left",
	"text-align", "text-decoration", "text-indent", "text-transform", "text-overflow", "vertical-align", "white-space",
	"word-break", "word-wrap", "word-spacing", "overflow-wrap", "table-layout", "direction", "caption-side", "empty-cells",
	"mso-hide", "mso-line-height-rule", "-webkit-text-size-adjust", "-ms-text-size-adjust",
}

var dangerousCSS = regexp.MustCompile(`(?i)(expression\s*\(|javascript:|vbscript:|behavior\s*:|-moz-binding|@import)`)

// safeCSSValue rejects dangerous constructs. CSS escapes ("expr\65ssion",
// "u\72l(") are rejected outright in inline styles: legitimate mail doesn't
// need them there, and they would hide constructs from these checks.
func safeCSSValue(v string) bool { return !strings.Contains(v, "\\") && !dangerousCSS.MatchString(v) }

var cssEscapeRe = regexp.MustCompile(`\\([0-9a-fA-F]{1,6})[ \t\r\n\f]?|\\(.)`)

// cssUnescape decodes CSS escapes so that checks see the real text.
func cssUnescape(css string) string {
	if !strings.Contains(css, "\\") {
		return css
	}
	return cssEscapeRe.ReplaceAllStringFunc(css, func(m string) string {
		sm := cssEscapeRe.FindStringSubmatch(m)
		if sm[1] != "" {
			n, err := strconv.ParseUint(sm[1], 16, 32)
			if err != nil || n == 0 || n > 0x10FFFF || (n >= 0xD800 && n <= 0xDFFF) {
				return "\uFFFD"
			}
			return string(rune(n))
		}
		return sm[2]
	})
}

func emailPolicy() *bluemonday.Policy {
	policyOnce.Do(func() {
		p := bluemonday.UGCPolicy()
		p.AllowElements("table", "thead", "tbody", "tfoot", "tr", "td", "th", "caption", "colgroup", "col",
			"center", "font", "span", "div", "section", "header", "footer", "article", "main", "style", "u", "s", "small", "big", "wbr")
		p.AllowAttrs("width", "height", "align", "valign", "bgcolor", "background", "border", "cellpadding", "cellspacing",
			"colspan", "rowspan", "nowrap", "dir", "lang", "class", "id", "role", "title").Globally()
		p.AllowAttrs("color", "face", "size").OnElements("font")
		p.AllowAttrs("start", "type").OnElements("ol", "ul", "li")
		p.AllowStyles(styleProps...).MatchingHandler(safeCSSValue).Globally()
		// data: is only allowed for base64 raster/SVG images (AllowDataURIImages);
		// listing "data" in AllowURLSchemes would override that check and let
		// data:text/html links through.
		p.AllowURLSchemes("http", "https", "mailto", "tel", "cid")
		p.AllowDataURIImages()
		p.AllowAttrs("src", "alt", "width", "height").OnElements("img")
		p.AllowAttrs("srcset").Matching(safeSrcset).OnElements("img")
		p.AllowAttrs("sizes").Matching(regexp.MustCompile(`^[a-zA-Z0-9 ()%.,:-]+$`)).OnElements("img")
		p.RequireNoFollowOnLinks(false)
		p.RequireNoReferrerOnLinks(true)
		p.AddTargetBlankToFullyQualifiedLinks(true)
		// <style> blocks are kept (newsletters depend on them); their text is
		// scrubbed of dangerous constructs below. Scripts stay forbidden
		// because the policy never allows the element.
		p.AllowUnsafe(true)
		policy = p
	})
	return policy
}

var styleBlockRe = regexp.MustCompile(`(?is)<style[^>]*>(.*?)(?:</style\s*>|$)`)

// safeSrcset accepts only http(s) image candidates.
var safeSrcset = regexp.MustCompile(`^\s*https?://[^\s,"'<>]+(\s+\d+(\.\d+)?[wx])?(\s*,\s*https?://[^\s,"'<>]+(\s+\d+(\.\d+)?[wx])?)*\s*$`)

// SanitizeHTML makes an email HTML body safe to render (in a sandboxed
// iframe): scripts, iframes, forms, objects and event handlers are removed;
// layout markup, inline styles and images are kept.
func SanitizeHTML(s string) string {
	s = scrubStyleBlocks(s)
	// Scrub again after sanitizing: the parser may see style blocks the
	// regex didn't (e.g. "</style >" or an unclosed <style>), and its output
	// is normalized.
	return strings.TrimSpace(scrubStyleBlocks(emailPolicy().Sanitize(s)))
}

func scrubStyleBlocks(s string) string {
	return styleBlockRe.ReplaceAllStringFunc(s, func(block string) string {
		m := styleBlockRe.FindStringSubmatch(block)
		css := dangerousCSS.ReplaceAllString(cssUnescape(m[1]), "/*removed*/")
		css = strings.ReplaceAll(css, "<", "")
		return "<style>" + css + "</style>"
	})
}

// A remote reference is protocol-relative ("//host") or has an http(s)
// scheme in any spelling browsers accept: "https://host", "https:\\host"
// and even "https:host" all load from host (the page's base URL isn't
// http(s), so the scheme can't make them relative).
var (
	remoteRe      = regexp.MustCompile(`(?i)\b(src|background)\s*=\s*["']?\s*(https?:|//)|\bsrcset\s*=\s*["']?[^"'>]*?\bhttps?:|url\(\s*(&#34;|&#39;|&quot;|["'])?\s*(https?:|//)|image-set\(`)
	imageSetRe    = regexp.MustCompile(`(?i)(-webkit-)?image-set\([^)]*\)`)
	remoteAttrRe  = regexp.MustCompile(`(?i)\s(src|background)\s*=\s*"\s*(https?:|//)[^"]*"`)
	remoteSetRe   = regexp.MustCompile(`(?i)\ssrcset\s*=\s*"[^"]*"`)
	remoteURLFnRe = regexp.MustCompile(`(?i)url\(\s*(&#34;|&#39;|&quot;|["'])?\s*(https?:|//)[^)]*\)`)
)

// HasRemoteContent reports whether HTML loads remote resources (images,
// srcset candidates, CSS url()).
func HasRemoteContent(s string) bool { return remoteRe.MatchString(s) }

// BlockRemoteContent strips remote resource references from sanitized
// HTML: remote src/background attributes, srcset and CSS url(). Links are
// kept. It reports whether anything was removed.
func BlockRemoteContent(s string) (string, bool) {
	if !HasRemoteContent(s) {
		return s, false
	}
	out := remoteAttrRe.ReplaceAllString(s, ` data-gomail-blocked=""`)
	out = remoteSetRe.ReplaceAllString(out, "")
	out = remoteURLFnRe.ReplaceAllString(out, "none")
	out = imageSetRe.ReplaceAllString(out, "none")
	return out, out != s
}

// HTMLToText converts HTML to readable plain text (for search, snippets,
// and the text alternative of outgoing mail).
func HTMLToText(s string) string {
	z := nethtml.NewTokenizer(strings.NewReader(s))
	var b strings.Builder
	skip := 0
	var href []string
	newline := func() {
		str := b.String()
		if !strings.HasSuffix(str, "\n") && len(str) > 0 {
			b.WriteString("\n")
		}
	}
	for {
		tt := z.Next()
		switch tt {
		case nethtml.ErrorToken:
			return cleanText(b.String())
		case nethtml.TextToken:
			if skip == 0 {
				t := strings.Join(strings.Fields(string(z.Text())), " ")
				if t != "" {
					str := b.String()
					if len(str) > 0 && !strings.HasSuffix(str, "\n") && !strings.HasSuffix(str, " ") {
						b.WriteString(" ")
					}
					b.WriteString(t)
				}
			}
		case nethtml.StartTagToken, nethtml.SelfClosingTagToken, nethtml.EndTagToken:
			name, hasAttr := z.TagName()
			tag := string(name)
			switch tag {
			case "script", "style", "head", "title":
				if tt == nethtml.StartTagToken {
					skip++
				} else if tt == nethtml.EndTagToken && skip > 0 {
					skip--
				}
			case "br":
				b.WriteString("\n")
			case "p", "div", "tr", "table", "h1", "h2", "h3", "h4", "h5", "h6", "blockquote", "section", "article", "header", "footer", "ul", "ol":
				newline()
				if tag == "p" && tt == nethtml.EndTagToken {
					b.WriteString("\n")
				}
			case "li":
				if tt == nethtml.StartTagToken {
					newline()
					b.WriteString("• ")
				}
			case "td", "th":
				if tt == nethtml.EndTagToken {
					b.WriteString(" ")
				}
			case "a":
				if tt == nethtml.StartTagToken {
					link := ""
					for hasAttr {
						var k, v []byte
						k, v, hasAttr = z.TagAttr()
						if string(k) == "href" {
							link = string(v)
						}
					}
					href = append(href, link)
				} else if tt == nethtml.EndTagToken && len(href) > 0 {
					link := href[len(href)-1]
					href = href[:len(href)-1]
					if strings.HasPrefix(link, "http") && !strings.Contains(b.String()[max(0, b.Len()-len(link)-2):], link) {
						b.WriteString(" <" + link + ">")
					}
				}
			}
		}
	}
}

var blankLines = regexp.MustCompile(`\n{3,}`)

func cleanText(s string) string {
	s = html.UnescapeString(s)
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t ")
	}
	s = strings.Join(lines, "\n")
	return strings.TrimSpace(blankLines.ReplaceAllString(s, "\n\n"))
}

// TextToHTML renders plain text as simple HTML (escaped, line breaks kept).
func TextToHTML(s string) string {
	esc := html.EscapeString(s)
	esc = strings.ReplaceAll(esc, "\r\n", "\n")
	return "<div style=\"white-space: pre-wrap\">" + esc + "</div>"
}

// Snippet derives a short preview from text.
func Snippet(text string, n int) string {
	r := []rune(strings.Join(strings.Fields(text), " "))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}
