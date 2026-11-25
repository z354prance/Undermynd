// Package deps pins the outside libraries the built-in downloaders use until
// the code that uses them lands (temporary).
package deps

import (
	_ "github.com/akalin/gopar/par2"
	_ "github.com/anacrolix/torrent"
	_ "github.com/anacrolix/torrent/storage"
	_ "github.com/nwaples/rardecode/v2"
	_ "golang.zx2c4.com/wireguard/conn"
	_ "golang.zx2c4.com/wireguard/device"
	_ "golang.zx2c4.com/wireguard/tun/netstack"
)
