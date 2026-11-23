package access

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"html"
	"mime"
	"net/mail"
	"net/url"
	"strings"
	"time"
)

// A letter is one email, written once and rendered as plain text and as HTML.
// Everything the visitor typed is escaped when it becomes HTML.
type letter struct {
	Title   string      // heading inside the email
	Intro   []string    // opening paragraphs
	Details [][2]string // label, value
	Steps   []string    // numbered, in order
	Button  [2]string   // label, URL
	Parts   [][2]string // titled sections: title, text
	Links   [][2]string // label, URL
	Note    string      // closing small print
}

type notice struct {
	to, subject string
	l           letter
}

func (l letter) text() string {
	var b strings.Builder
	b.WriteString(l.Title + "\n\n")
	for _, p := range l.Intro {
		b.WriteString(p + "\n\n")
	}
	for _, d := range l.Details {
		b.WriteString(d[0] + ": " + d[1] + "\n")
	}
	if len(l.Details) > 0 {
		b.WriteString("\n")
	}
	for i, s := range l.Steps {
		fmt.Fprintf(&b, "%d. %s\n", i+1, s)
	}
	if len(l.Steps) > 0 {
		b.WriteString("\n")
	}
	if l.Button[1] != "" {
		b.WriteString(l.Button[0] + ":\n" + l.Button[1] + "\n\n")
	}
	for _, p := range l.Parts {
		b.WriteString(p[0] + "\n" + p[1] + "\n\n")
	}
	for _, k := range l.Links {
		b.WriteString(k[0] + ": " + k[1] + "\n")
	}
	if len(l.Links) > 0 {
		b.WriteString("\n")
	}
	if l.Note != "" {
		b.WriteString(l.Note + "\n")
	}
	b.WriteString("\n-- HyveMynd\n")
	return b.String()
}

func esc(s string) string { return html.EscapeString(s) }

// safeLink allows only http(s) links in the HTML version.
func safeLink(s string) string {
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return ""
	}
	return esc(u.String())
}

func (l letter) html() string {
	const ink, dim, line, accent = "#1b1f23", "#5b6470", "#e3e6ea", "#7c5cf0"
	var b strings.Builder
	b.WriteString(`<!doctype html><html><body style="margin:0;padding:0;background:#f2f4f6;">`)
	b.WriteString(`<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="background:#f2f4f6;padding:24px 12px;"><tr><td align="center">`)
	b.WriteString(`<table role="presentation" width="560" cellpadding="0" cellspacing="0" style="max-width:560px;width:100%;background:#ffffff;border-radius:8px;overflow:hidden;font-family:-apple-system,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;color:` + ink + `;">`)
	// The site's header: the name on graphite, the four media colours under it.
	b.WriteString(`<tr><td style="background:#1e2022;padding:16px 28px;font:800 19px -apple-system,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;color:#ffffff;letter-spacing:-0.01em;">HyveMynd</td></tr>`)
	b.WriteString(`<tr><td style="padding:0;font-size:0;line-height:0;"><table role="presentation" width="100%" cellpadding="0" cellspacing="0"><tr>` +
		`<td style="height:4px;background:#409cff;"></td><td style="height:4px;background:#d4af37;"></td><td style="height:4px;background:#ad92ec;"></td><td style="height:4px;background:#3fb950;"></td></tr></table></td></tr>`)
	b.WriteString(`<tr><td style="padding:28px 28px 8px 28px;">`)
	b.WriteString(`<h1 style="margin:0 0 16px 0;font-size:21px;line-height:1.3;color:` + ink + `;">` + esc(l.Title) + `</h1>`)
	for _, p := range l.Intro {
		b.WriteString(`<p style="margin:0 0 14px 0;font-size:15px;line-height:1.55;">` + esc(p) + `</p>`)
	}
	if len(l.Details) > 0 {
		b.WriteString(`<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="margin:6px 0 18px 0;border:1px solid ` + line + `;border-radius:6px;">`)
		for i, d := range l.Details {
			border := ""
			if i > 0 {
				border = "border-top:1px solid " + line + ";"
			}
			b.WriteString(`<tr><td style="padding:9px 14px;width:34%;font-size:13px;color:` + dim + `;` + border + `">` + esc(d[0]) + `</td><td style="padding:9px 14px;font-size:14px;font-weight:600;word-break:break-word;` + border + `">` + esc(d[1]) + `</td></tr>`)
		}
		b.WriteString(`</table>`)
	}
	if len(l.Steps) > 0 {
		b.WriteString(`<table role="presentation" cellpadding="0" cellspacing="0" style="margin:4px 0 14px 0;">`)
		for i, s := range l.Steps {
			b.WriteString(fmt.Sprintf(`<tr><td valign="top" style="padding:0 12px 10px 0;"><div style="width:24px;height:24px;border-radius:12px;background:#efeaff;color:#5b3fd0;font:700 13px/24px Arial,sans-serif;text-align:center;">%d</div></td><td style="padding:2px 0 10px 0;font-size:15px;line-height:1.5;">%s</td></tr>`, i+1, esc(s)))
		}
		b.WriteString(`</table>`)
	}
	if href := safeLink(l.Button[1]); href != "" {
		b.WriteString(`<table role="presentation" cellpadding="0" cellspacing="0" style="margin:8px 0 8px 0;"><tr><td style="background:` + accent + `;border-radius:6px;"><a href="` + href + `" style="display:inline-block;padding:12px 22px;font:600 15px Arial,sans-serif;color:#ffffff;text-decoration:none;">` + esc(l.Button[0]) + `</a></td></tr></table>`)
		b.WriteString(`<p style="margin:6px 0 16px 0;font-size:12px;line-height:1.5;color:` + dim + `;">If the button does not work, copy this address into your browser:<br><span style="word-break:break-all;">` + esc(l.Button[1]) + `</span></p>`)
	}
	for _, p := range l.Parts {
		b.WriteString(`<h2 style="margin:20px 0 4px 0;font-size:15px;color:` + ink + `;">` + esc(p[0]) + `</h2><p style="margin:0 0 6px 0;font-size:14px;line-height:1.55;color:#2d333a;">` + esc(p[1]) + `</p>`)
	}
	if len(l.Links) > 0 {
		b.WriteString(`<p style="margin:18px 0 6px 0;font-size:14px;line-height:1.7;">`)
		for _, k := range l.Links {
			if href := safeLink(k[1]); href != "" {
				b.WriteString(`<a href="` + href + `" style="color:#5b3fd0;">` + esc(k[0]) + `</a><br>`)
			}
		}
		b.WriteString(`</p>`)
	}
	b.WriteString(`</td></tr>`)
	if l.Note != "" {
		b.WriteString(`<tr><td style="padding:8px 28px 24px 28px;"><p style="margin:0;padding-top:16px;border-top:1px solid ` + line + `;font-size:12px;line-height:1.55;color:` + dim + `;">` + esc(l.Note) + `</p></td></tr>`)
	} else {
		b.WriteString(`<tr><td style="height:20px;"></td></tr>`)
	}
	b.WriteString(`</table></td></tr></table></body></html>`)
	return b.String()
}

func wrap76(s string) string {
	var b strings.Builder
	for len(s) > 76 {
		b.WriteString(s[:76] + "\r\n")
		s = s[76:]
	}
	return b.String() + s + "\r\n"
}

// message builds the full email: headers, plain text and HTML in one multipart body.
func message(s Settings, to, subject string, l letter) (string, error) {
	var rnd [12]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return "", err
	}
	id := hex.EncodeToString(rnd[:])
	domain := "localhost"
	if a, err := mail.ParseAddress(s.From); err == nil {
		if i := strings.LastIndex(a.Address, "@"); i >= 0 {
			domain = a.Address[i+1:]
		}
	}
	from := (&mail.Address{Name: "HyveMynd", Address: s.From}).String()
	crlf := func(t string) string { return strings.ReplaceAll(strings.ReplaceAll(t, "\r\n", "\n"), "\n", "\r\n") }
	_ = crlf
	var b strings.Builder
	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + to + "\r\n")
	b.WriteString("Subject: " + mime.QEncoding.Encode("utf-8", subject) + "\r\n")
	b.WriteString("Date: " + time.Now().Format(time.RFC1123Z) + "\r\n")
	b.WriteString("Message-ID: <" + id + "@" + domain + ">\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: multipart/alternative; boundary=\"um-" + id + "\"\r\n\r\n")
	for _, part := range [][2]string{{"text/plain", l.text()}, {"text/html", l.html()}} {
		b.WriteString("--um-" + id + "\r\nContent-Type: " + part[0] + "; charset=UTF-8\r\nContent-Transfer-Encoding: base64\r\n\r\n")
		b.WriteString(wrap76(base64.StdEncoding.EncodeToString([]byte(part[1]))))
	}
	b.WriteString("--um-" + id + "--\r\n")
	return b.String(), nil
}
