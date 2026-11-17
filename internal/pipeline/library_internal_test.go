package pipeline

import (
	"testing"

	"github.com/z354prance/undermynd/internal/library"
)

func TestByOtherTitle(t *testing.T) {
	titles := map[string][]string{"Seitokai ni mo Ana wa Aru": {"Even the Student Council Has Its Holes!"}, "Blue Box": {"Ao no Hako"}}
	r, _ := library.Parse("[Erai-raws] Even the Student Council Has Its Holes! - 07 [1080p].mkv")
	if f := byOtherTitle(r, titles); f != "Seitokai ni mo Ana wa Aru" {
		t.Fatalf("got %q", f)
	}
	r, _ = library.Parse("[SubsPlease] Something Else - 01 (1080p).mkv")
	if f := byOtherTitle(r, titles); f != "" {
		t.Fatalf("got %q", f)
	}
}
