// Package notify tells webhooks, Discord and ntfy about what happens
// (roadmap I1): grabs, imports, failed downloads, new requests.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/z354prance/undermynd/internal/config"
)

// Event is one thing to tell about.
type Event struct {
	Event    string    `json:"event"` // grabbed, imported, failed, request, dry run, replaced, test
	Item     string    `json:"item"`
	Release  string    `json:"release,omitempty"`
	Quality  string    `json:"quality,omitempty"`
	Category string    `json:"category,omitempty"`
	Note     string    `json:"note,omitempty"`
	At       time.Time `json:"at"`
}

// Text is the event as one line.
func (e Event) Text() string {
	var b strings.Builder
	b.WriteString(strings.ToUpper(e.Event[:1]) + e.Event[1:] + ": " + e.Item)
	if e.Quality != "" {
		b.WriteString(" (" + e.Quality + ")")
	}
	if e.Release != "" && e.Event != "request" {
		b.WriteString(" · " + e.Release)
	}
	if e.Note != "" && (e.Event == "failed" || e.Event == "request") {
		b.WriteString(" · " + e.Note)
	}
	return b.String()
}

// Notifier sends events in the background.
type Notifier struct {
	Cfg  *config.Config
	HTTP *http.Client
	Log  *log.Logger
	ch   chan Event
}

// Start starts sending.
func (n *Notifier) Start(ctx context.Context) {
	n.ch = make(chan Event, 200)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case e := <-n.ch:
				for _, t := range n.Cfg.Get().Notifications {
					if t.Enabled && wants(t, e.Event) {
						if err := n.Send(ctx, t, e); err != nil {
							n.Log.Printf("notify %s: %v", t.Name, err)
						}
					}
				}
			}
		}
	}()
}

func wants(t config.Notification, ev string) bool {
	if len(t.Events) == 0 {
		return true
	}
	for _, x := range t.Events {
		if strings.EqualFold(x, ev) {
			return true
		}
	}
	return false
}

// Notify queues an event; it never blocks (a full queue drops it).
func (n *Notifier) Notify(e Event) {
	if n == nil || n.ch == nil {
		return
	}
	if e.At.IsZero() {
		e.At = time.Now()
	}
	select {
	case n.ch <- e:
	default:
	}
}

// Send delivers one event to one target, trying three times.
func (n *Notifier) Send(ctx context.Context, t config.Notification, e Event) error {
	if e.At.IsZero() {
		e.At = time.Now()
	}
	var body []byte
	ctype := "application/json"
	headers := map[string]string{}
	switch t.Kind {
	case "discord":
		body, _ = json.Marshal(map[string]any{"username": "Undermynd", "content": e.Text()})
	case "ntfy":
		body = []byte(e.Text())
		ctype = "text/plain"
		headers["Title"] = "Undermynd: " + e.Event
		headers["Tags"] = map[string]string{"failed": "warning", "imported": "white_check_mark", "request": "inbox_tray"}[e.Event]
	case "webhook", "":
		body, _ = json.Marshal(e)
	default:
		return fmt.Errorf("unknown kind %q", t.Kind)
	}
	var last error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt) * 5 * time.Second):
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, string(t.URL), bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", ctype)
		req.Header.Set("User-Agent", "Undermynd")
		for k, v := range headers {
			if v != "" {
				req.Header.Set(k, v)
			}
		}
		resp, err := n.HTTP.Do(req)
		if err != nil {
			last = err
			continue
		}
		resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return nil
		}
		last = fmt.Errorf("HTTP %d", resp.StatusCode)
		if resp.StatusCode >= 400 && resp.StatusCode < 500 && resp.StatusCode != 429 {
			return last // the address or the message is wrong: trying again will not help
		}
	}
	return last
}
