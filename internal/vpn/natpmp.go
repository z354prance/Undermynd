package vpn

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"time"
)

// natPMP asks the VPN gateway (through the tunnel) to forward a public port
// to this end, as Proton VPN's P2P servers do (RFC 6886). proto is "udp" or
// "tcp"; the mapping lasts lifetime and must be renewed before it ends. It
// returns the public port.
func natPMP(ctx context.Context, dial func(context.Context, string, string) (net.Conn, error), gateway, proto string, lifetime time.Duration) (int, error) {
	op := byte(1)
	if proto == "tcp" {
		op = 2
	}
	c, err := dial(ctx, "udp", net.JoinHostPort(gateway, "5351"))
	if err != nil {
		return 0, err
	}
	defer c.Close()
	req := make([]byte, 12)
	req[1] = op
	binary.BigEndian.PutUint16(req[4:], 1) // internal port, as Proton asks
	binary.BigEndian.PutUint16(req[6:], 0) // any public port
	binary.BigEndian.PutUint32(req[8:], uint32(lifetime/time.Second))
	wait := 250 * time.Millisecond
	buf := make([]byte, 64)
	for try := 0; try < 4; try++ {
		if _, err := c.Write(req); err != nil {
			return 0, err
		}
		dl := time.Now().Add(wait)
		if d, ok := ctx.Deadline(); ok && d.Before(dl) {
			dl = d
		}
		c.SetReadDeadline(dl)
		n, err := c.Read(buf)
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() && ctx.Err() == nil {
				wait *= 2
				continue
			}
			return 0, err
		}
		if n < 16 || buf[1] != 128+op {
			continue
		}
		if code := binary.BigEndian.Uint16(buf[2:]); code != 0 {
			return 0, fmt.Errorf("the VPN server refused port forwarding (code %d)", code)
		}
		return int(binary.BigEndian.Uint16(buf[10:])), nil
	}
	return 0, errors.New("the VPN server did not answer the port-forwarding request: pick a P2P server with NAT-PMP on")
}
