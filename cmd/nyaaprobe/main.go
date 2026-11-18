package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/z354prance/undermynd/internal/shows"
)

func main() {
	hc := &http.Client{Timeout: 30 * time.Second}
	n := shows.Nyaa{HTTP: hc}
	u := n.SearchURL("gorilla no kami")
	resp, err := hc.Get(u)
	if err != nil {
		fmt.Println("GET error:", err)
		return
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	fmt.Println("status", resp.StatusCode, "content-type", resp.Header.Get("Content-Type"), "bytes", len(b))
	if len(b) > 600 {
		fmt.Println(string(b[:600]))
	} else {
		fmt.Println(string(b))
	}
	res, err := shows.ParseNyaa(b)
	fmt.Println("parsed", len(res), err)
	for i, r := range res {
		if i > 4 {
			break
		}
		fmt.Printf("%q ok=%v group=%q ep=%d res=%q seed=%d link=%q\n", r.Title, r.OK, r.Parsed.Group, r.Parsed.Episode, r.Parsed.Res, r.Seeders, r.Link)
	}
	_, err = n.Search(context.Background(), "Seitokai ni mo Ana wa Aru")
	fmt.Println("search err:", err)
}
