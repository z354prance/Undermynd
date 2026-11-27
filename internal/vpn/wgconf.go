package vpn

import (
	"bufio"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
)

// WGConf is what HyveMynd needs from a WireGuard file.
type WGConf struct {
	Private  []byte
	Address  netip.Addr // this end's IPv4 address in the tunnel
	DNS      []netip.Addr
	Public   []byte // the server's key
	Shared   []byte // preshared key, if any
	Endpoint string // host:port
	MTU      int
}

// ParseWG reads a WireGuard file as providers give them out. Only IPv4 is
// used inside the tunnel, so an IPv6 address or DNS server is skipped.
func ParseWG(text string) (WGConf, error) {
	var c WGConf
	section := ""
	peers := 0
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if i := strings.IndexAny(line, "#;"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") {
			section = strings.ToLower(strings.Trim(line, "[] "))
			if section == "peer" {
				peers++
			}
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k, v = strings.ToLower(strings.TrimSpace(k)), strings.TrimSpace(v)
		if section == "peer" && peers > 1 {
			continue // the first server is used
		}
		var err error
		name := map[string]string{"privatekey": "PrivateKey", "publickey": "PublicKey", "presharedkey": "PresharedKey"}[k]
		switch section + "." + k {
		case "interface.privatekey":
			c.Private, err = key(v)
		case "interface.address":
			for _, a := range splitList(v) {
				if p, perr := netip.ParsePrefix(a); perr == nil && p.Addr().Is4() && !c.Address.IsValid() {
					c.Address = p.Addr()
				} else if ad, aerr := netip.ParseAddr(a); aerr == nil && ad.Is4() && !c.Address.IsValid() {
					c.Address = ad
				}
			}
		case "interface.dns":
			for _, a := range splitList(v) {
				if ad, derr := netip.ParseAddr(a); derr == nil && ad.Is4() {
					c.DNS = append(c.DNS, ad)
				}
			}
		case "interface.mtu":
			c.MTU, _ = strconv.Atoi(v)
		case "peer.publickey":
			c.Public, err = key(v)
		case "peer.presharedkey":
			c.Shared, err = key(v)
		case "peer.endpoint":
			c.Endpoint = v
		}
		if err != nil {
			return c, fmt.Errorf("the WireGuard file's %s could not be read", name)
		}
	}
	switch {
	case c.Private == nil:
		return c, errors.New("the WireGuard file has no PrivateKey: download it again from your provider")
	case !c.Address.IsValid():
		return c, errors.New("the WireGuard file has no IPv4 Address")
	case c.Public == nil:
		return c, errors.New("the WireGuard file has no server PublicKey")
	case c.Endpoint == "":
		return c, errors.New("the WireGuard file has no server Endpoint")
	}
	if _, _, err := net.SplitHostPort(c.Endpoint); err != nil {
		return c, errors.New("the WireGuard file's Endpoint is not host:port")
	}
	if c.MTU <= 0 || c.MTU > 1420 {
		c.MTU = 1420
	}
	return c, nil
}

func key(v string) ([]byte, error) {
	b, err := base64.StdEncoding.DecodeString(v)
	if err != nil || len(b) != 32 {
		return nil, errors.New("bad key")
	}
	return b, nil
}

func splitList(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
