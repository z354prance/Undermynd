// Package usenet is HyveMynd's own usenet downloader: NZB in, finished
// files out. It fetches articles from the owner's providers (main ones
// first, backups for articles the main ones lack), decodes yEnc, restores
// scrambled file names, repairs with par2 when something is missing or
// damaged, and unpacks RAR archives.
package usenet

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/textproto"
	"strings"
	"sync"
	"time"

	"github.com/z354prance/undermynd/internal/config"
)

// ErrNoArticle means the provider does not have the article (430).
var ErrNoArticle = errors.New("article not found")

// errAuth means the provider refused the login.
type errAuth struct{ msg string }

func (e errAuth) Error() string { return e.msg }

// conn is one signed-in connection to a provider.
type conn struct {
	nc   net.Conn
	tp   *textproto.Conn
	used time.Time
}

func (c *conn) close() { c.tp.Close() }

// Dialer opens network connections (tests replace it).
type Dialer func(ctx context.Context, network, addr string) (net.Conn, error)

func dial(ctx context.Context, d Dialer, s config.NewsServer) (*conn, error) {
	if d == nil {
		nd := &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}
		d = nd.DialContext
	}
	port := s.Port
	if port == 0 {
		port = 119
		if s.TLS {
			port = 563
		}
	}
	addr := net.JoinHostPort(s.Host, fmt.Sprint(port))
	nc, err := d(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("%s: could not connect: %w", s.Label(), err)
	}
	if s.TLS {
		tc := tls.Client(nc, &tls.Config{ServerName: s.Host, MinVersion: tls.VersionTLS12, InsecureSkipVerify: s.SkipVerify})
		if dl, ok := ctx.Deadline(); ok {
			tc.SetDeadline(dl)
		}
		if err := tc.HandshakeContext(ctx); err != nil {
			nc.Close()
			return nil, fmt.Errorf("%s: secure connection failed: %w", s.Label(), err)
		}
		tc.SetDeadline(time.Time{})
		nc = tc
	}
	c := &conn{nc: nc, tp: textproto.NewConn(nc), used: time.Now()}
	nc.SetDeadline(time.Now().Add(30 * time.Second))
	code, msg, err := c.tp.ReadCodeLine(0)
	if err != nil || (code != 200 && code != 201) {
		c.close()
		if err == nil {
			err = fmt.Errorf("%d %s", code, msg)
		}
		return nil, fmt.Errorf("%s: unexpected greeting: %v", s.Label(), err)
	}
	if s.User != "" {
		code, msg, err = c.cmd("AUTHINFO USER " + s.User)
		if err == nil && code == 381 {
			code, msg, err = c.cmd("AUTHINFO PASS " + string(s.Pass))
		}
		if err != nil {
			c.close()
			return nil, fmt.Errorf("%s: %w", s.Label(), err)
		}
		if code != 281 {
			c.close()
			return nil, errAuth{fmt.Sprintf("%s refused the login (%d %s)", s.Label(), code, strings.TrimSpace(msg))}
		}
	}
	nc.SetDeadline(time.Time{})
	return c, nil
}

func (c *conn) cmd(line string) (int, string, error) {
	id, err := c.tp.Cmd("%s", line)
	if err != nil {
		return 0, "", err
	}
	c.tp.StartResponse(id)
	defer c.tp.EndResponse(id)
	return c.tp.ReadCodeLine(0)
}

// body fetches an article's body (dot-unstuffed).
func (c *conn) body(msgID string, w io.Writer) error {
	c.nc.SetDeadline(time.Now().Add(60 * time.Second))
	defer c.nc.SetDeadline(time.Time{})
	id, err := c.tp.Cmd("BODY <%s>", strings.Trim(msgID, "<>"))
	if err != nil {
		return err
	}
	c.tp.StartResponse(id)
	defer c.tp.EndResponse(id)
	code, msg, err := c.tp.ReadCodeLine(0)
	if err != nil {
		return err
	}
	switch code {
	case 222:
	case 430, 423:
		return ErrNoArticle
	default:
		return fmt.Errorf("provider answered %d %s", code, msg)
	}
	_, err = io.Copy(w, c.tp.DotReader())
	c.used = time.Now()
	return err
}

// pool holds a provider's connections, at most Conns at once.
type pool struct {
	srv  config.NewsServer
	dial Dialer
	slot chan struct{}

	mu   sync.Mutex
	idle []*conn
	bad  string    // last login or connection failure
	off  time.Time // after a failure, rest until then
}

func newPool(s config.NewsServer, d Dialer) *pool {
	n := s.Conns
	if n <= 0 {
		n = 8
	}
	return &pool{srv: s, dial: d, slot: make(chan struct{}, n)}
}

// get takes a connection, waiting for a free slot.
func (p *pool) get(ctx context.Context) (*conn, error) {
	select {
	case p.slot <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	p.mu.Lock()
	if time.Now().Before(p.off) {
		why := p.bad
		p.mu.Unlock()
		<-p.slot
		return nil, fmt.Errorf("%s is resting after: %s", p.srv.Label(), why)
	}
	for len(p.idle) > 0 {
		c := p.idle[len(p.idle)-1]
		p.idle = p.idle[:len(p.idle)-1]
		if time.Since(c.used) < 50*time.Second {
			p.mu.Unlock()
			return c, nil
		}
		c.close() // providers drop idle connections; do not trust old ones
	}
	p.mu.Unlock()
	dctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	c, err := dial(dctx, p.dial, p.srv)
	cancel()
	if err != nil {
		p.mu.Lock()
		p.bad = err.Error()
		rest := 30 * time.Second
		var ae errAuth
		if errors.As(err, &ae) {
			rest = 5 * time.Minute
		}
		p.off = time.Now().Add(rest)
		p.mu.Unlock()
		<-p.slot
		return nil, err
	}
	p.mu.Lock()
	p.bad = ""
	p.mu.Unlock()
	return c, nil
}

// put returns a connection (nil or broken: dropped).
func (p *pool) put(c *conn, ok bool) {
	if c != nil {
		if ok {
			p.mu.Lock()
			p.idle = append(p.idle, c)
			p.mu.Unlock()
		} else {
			c.close()
		}
	}
	<-p.slot
}

func (p *pool) closeAll() {
	p.mu.Lock()
	for _, c := range p.idle {
		c.close()
	}
	p.idle = nil
	p.mu.Unlock()
}

func (p *pool) problem() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.bad
}

// Test signs in to a provider and reports what went wrong, if anything.
func Test(ctx context.Context, s config.NewsServer, d Dialer) error {
	c, err := dial(ctx, d, s)
	if err != nil {
		return err
	}
	c.cmd("QUIT")
	c.close()
	return nil
}
