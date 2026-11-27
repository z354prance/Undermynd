package web

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/z354prance/undermynd/internal/bt"
	"github.com/z354prance/undermynd/internal/job"
	"github.com/z354prance/undermynd/internal/pipeline"
	"github.com/z354prance/undermynd/internal/usenet"
)

// clientItem is one download on the Clients page.
type clientItem struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Category string    `json:"category"`
	Kind     string    `json:"kind"`  // torrent | usenet
	Where    string    `json:"where"` // TorBox | Torrent | Usenet | qBittorrent | NZBGet | "" (not sent yet)
	State    string    `json:"state"`
	Note     string    `json:"note,omitempty"`
	Progress float64   `json:"progress"`
	Size     int64     `json:"size"`
	Down     int64     `json:"down,omitempty"` // bytes a second
	Up       int64     `json:"up,omitempty"`
	ETA      int64     `json:"eta,omitempty"` // seconds
	Peers    int       `json:"peers,omitempty"`
	Seeds    int       `json:"seeds,omitempty"`
	Stage    string    `json:"stage,omitempty"`
	Position int       `json:"position,omitempty"`
	Paused   bool      `json:"paused,omitempty"`
	Main     int64     `json:"fromMain,omitempty"`
	Backup   int64     `json:"fromBackup,omitempty"`
	Can      []string  `json:"can"` // actions offered
	When     time.Time `json:"when"`
	Source   string    `json:"source"`
}

func whereName(b string) string {
	switch b {
	case "torbox":
		return "TorBox"
	case bt.Name:
		return "Torrent"
	case usenet.Name:
		return "Usenet"
	case "qbittorrent":
		return "qBittorrent"
	case "nzbget":
		return "NZBGet"
	}
	return ""
}

func (s *Server) adminClients(w http.ResponseWriter, r *http.Request) {
	var tor map[string]bt.Detail
	var news map[string]usenet.Detail
	out := struct {
		Items   []clientItem `json:"items"`
		History []clientItem `json:"history"`
		Torrent bt.Stats     `json:"torrent"`
		Usenet  usenet.Stats `json:"usenet"`
		TorBox  struct {
			Set    bool `json:"set"`
			Active int  `json:"active"`
		} `json:"torbox"`
		Speed      any      `json:"speed"`
		Categories []string `json:"categories"`
		Plain      []string `json:"plain"`
		Builtin    struct {
			Torrent bool `json:"torrent"`
			Usenet  bool `json:"usenet"`
		} `json:"builtin"`
	}{Items: []clientItem{}, History: []clientItem{}}
	c := s.Cfg.Get()
	if s.VPN != nil && s.VPN.Client != nil {
		tor = s.VPN.Client.Details()
		out.Torrent = s.VPN.Client.Stats()
	}
	if s.News != nil {
		news = s.News.Details()
		out.Usenet = s.News.Stats()
	}
	out.TorBox.Set = c.TorBox.On()
	out.Speed = c.Speed
	out.Builtin.Torrent, out.Builtin.Usenet = c.VPN.On && s.VPN != nil, c.Usenet.Ready() && s.News != nil
	for _, cat := range c.Categories {
		out.Categories = append(out.Categories, cat.Name)
		if cat.Plain() {
			out.Plain = append(out.Plain, cat.Name)
		}
	}
	cutoff := time.Now().Add(-72 * time.Hour)
	for _, j := range s.Engine.Store.List() {
		it := clientItem{ID: j.ID, Name: j.Name, Category: j.Category, Kind: j.Kind, Where: whereName(j.Backend), State: string(j.State),
			Note: j.Reason, Progress: j.Progress, Size: j.Size, When: j.StateChanged, Source: j.Source, Can: []string{}}
		if it.Kind == "" {
			it.Kind = "torrent"
		}
		switch j.State {
		case job.Received, job.Submitted, job.Downloading, job.Pulling:
		default:
			if j.Backend != "" && j.StateChanged.After(cutoff) && j.Source != "reencode" {
				out.History = append(out.History, it)
			}
			continue
		}
		if j.State == job.Pulling && j.PullTotal > 0 {
			it.Stage, it.Progress = "copying from TorBox", float64(j.PullDone)/float64(j.PullTotal)
		}
		switch j.Backend {
		case bt.Name:
			if d, ok := tor[j.RemoteID]; ok {
				it.Down, it.Up, it.Peers, it.Seeds, it.Paused = d.Down, d.Up, d.Peers, d.Seeds, d.Paused
				if d.Size > 0 {
					it.Size, it.Progress = d.Size, float64(d.Done)/float64(d.Size)
					if d.Down > 0 {
						it.ETA = (d.Size - d.Done) / d.Down
					}
				}
				switch {
				case d.Waiting:
					it.Stage = "waiting for the VPN"
				case d.Metadata:
					it.Stage = "finding peers"
				}
			}
		case usenet.Name:
			if d, ok := news[j.RemoteID]; ok {
				it.Stage, it.Progress, it.Down, it.Position, it.Paused, it.Main, it.Backup = d.Stage, d.Progress, d.Speed, d.Position, d.Paused, d.FromMain, d.FromBackup
				if d.Size > 0 {
					it.Size = d.Size
				}
				if d.Speed > 0 {
					it.ETA = int64((1 - d.Progress) * float64(d.Size) / float64(d.Speed))
				}
				if d.Note != "" {
					it.Note = d.Note
				}
			}
		case "torbox":
			out.TorBox.Active++
		}
		if j.Backend == bt.Name || j.Backend == usenet.Name {
			if it.Paused {
				it.Can = append(it.Can, "resume")
			} else if it.Stage != "repairing" && it.Stage != "unpacking" {
				it.Can = append(it.Can, "pause")
			}
		}
		if j.Backend == usenet.Name && it.Position > 1 {
			it.Can = append(it.Can, "top")
		}
		if j.Backend == "torbox" && j.State != job.Pulling &&
			((j.Kind == "usenet" && out.Builtin.Usenet) || (j.Kind != "usenet" && out.Builtin.Torrent)) {
			it.Can = append(it.Can, "builtin")
		}
		it.Can = append(it.Can, "remove")
		out.Items = append(out.Items, it)
	}
	sort.Slice(out.Items, func(i, k int) bool { return out.Items[i].When.Before(out.Items[k].When) })
	sort.Slice(out.History, func(i, k int) bool { return out.History[i].When.After(out.History[k].When) })
	if len(out.History) > 100 {
		out.History = out.History[:100]
	}
	for i := range out.History {
		h := &out.History[i]
		if h.State == string(job.Failed) {
			h.Can = []string{"retry"}
		}
	}
	writeJSON(w, out)
}

func (s *Server) adminClientAction(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var err error
	switch r.PathValue("action") {
	case "pause":
		err = s.Engine.Pause(id, true)
	case "resume":
		err = s.Engine.Pause(id, false)
	case "top":
		err = s.Engine.MoveToTop(id)
	case "builtin":
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		err = s.Engine.SendToBuiltIn(ctx, id)
	case "remove":
		err = s.Engine.Cancel(id)
	case "retry":
		err = s.Engine.Retry(id)
	default:
		jsonErr(w, http.StatusNotFound, "unknown action")
		return
	}
	if err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.Log.Printf("admin: %s on download %s", r.PathValue("action"), id[:min(8, len(id))])
	writeJSON(w, map[string]any{"ok": true})
}

// adminClientAdd takes a download by hand: a magnet, or a .torrent or .nzb file.
func (s *Server) adminClientAdd(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(16 << 20); err != nil {
		jsonErr(w, http.StatusBadRequest, "the upload could not be read")
		return
	}
	cat := strings.TrimSpace(r.FormValue("category"))
	if _, ok := s.Cfg.Get().Category(cat); !ok {
		jsonErr(w, http.StatusBadRequest, "choose a category")
		return
	}
	prefer := r.FormValue("prefer")
	if prefer != "" && prefer != "torbox" && prefer != "builtin" {
		jsonErr(w, http.StatusBadRequest, "unknown choice")
		return
	}
	var j job.Job
	var err error
	if mag := strings.TrimSpace(r.FormValue("magnet")); mag != "" {
		if !strings.HasPrefix(strings.ToLower(mag), "magnet:?") {
			jsonErr(w, http.StatusBadRequest, "that is not a magnet link")
			return
		}
		j, err = s.Engine.Submit(pipeline.SubmitRequest{Source: "manual", Category: cat, Magnet: mag, Prefer: prefer})
	} else {
		f, h, ferr := r.FormFile("file")
		if ferr != nil {
			jsonErr(w, http.StatusBadRequest, "paste a magnet link or choose a .torrent or .nzb file")
			return
		}
		defer f.Close()
		data, _ := io.ReadAll(io.LimitReader(f, 16<<20))
		switch strings.ToLower(filepath.Ext(h.Filename)) {
		case ".torrent":
			j, err = s.Engine.Submit(pipeline.SubmitRequest{Source: "manual", Category: cat, Torrent: data, Prefer: prefer})
		case ".nzb":
			j, err = s.Engine.SubmitNZB(pipeline.SubmitNZBRequest{Source: "manual", Category: cat, Name: strings.TrimSuffix(h.Filename, filepath.Ext(h.Filename)), NZB: data, Prefer: prefer})
		default:
			jsonErr(w, http.StatusBadRequest, "choose a .torrent or .nzb file")
			return
		}
	}
	if err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.Log.Printf("admin: added %s by hand (category %s)", j.Name, cat)
	writeJSON(w, map[string]any{"ok": true, "id": j.ID, "name": j.Name})
}

func (s *Server) adminClientLimits(w http.ResponseWriter, r *http.Request) {
	var in struct {
		TorrentDownKB int `json:"torrentDownKb"`
		TorrentUpKB   int `json:"torrentUpKb"`
		UsenetKB      int `json:"usenetKb"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12)).Decode(&in); err != nil {
		jsonErr(w, http.StatusBadRequest, "bad limits")
		return
	}
	cur := s.Cfg.Get()
	cur.Speed.TorrentDownKB, cur.Speed.TorrentUpKB, cur.Speed.UsenetKB = max(0, in.TorrentDownKB), max(0, in.TorrentUpKB), max(0, in.UsenetKB)
	if err := s.Cfg.Set(cur); err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// adminClientLog is one download's whole story: the pipeline's steps and
// the download client's own log, in time order, plus where it stands.
func (s *Server) adminClientLog(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	j, ok := s.Engine.Store.Get(id)
	if !ok {
		jsonErr(w, http.StatusNotFound, "no such download")
		return
	}
	type line struct {
		At   time.Time `json:"at"`
		Msg  string    `json:"msg"`
		From string    `json:"from"` // pipeline | client
	}
	out := struct {
		State    string    `json:"state"`
		Reason   string    `json:"reason,omitempty"`
		Where    string    `json:"where"`
		Attempts int       `json:"attempts,omitempty"`
		NextRun  time.Time `json:"nextRun,omitempty"`
		Folder   string    `json:"folder,omitempty"`
		Files    []string  `json:"files,omitempty"`
		Lines    []line    `json:"lines"`
	}{State: string(j.State), Reason: j.Reason, Where: whereName(j.Backend), Attempts: j.Attempts, NextRun: j.NextRun, Folder: j.SavePath, Files: j.Files, Lines: []line{}}
	out.Lines = append(out.Lines, line{At: j.Created, Msg: "received from " + j.Source, From: "pipeline"})
	for _, h := range j.History {
		msg := string(h.From) + " → " + string(h.To)
		if h.Reason != "" {
			msg += ": " + h.Reason
		}
		out.Lines = append(out.Lines, line{At: h.At, Msg: msg, From: "pipeline"})
	}
	switch j.Backend {
	case usenet.Name:
		if s.News != nil {
			for _, l := range s.News.LogOf(j.RemoteID) {
				out.Lines = append(out.Lines, line{At: l.At, Msg: l.Msg, From: "client"})
			}
		}
	case bt.Name:
		if s.VPN != nil && s.VPN.Client != nil {
			for _, l := range s.VPN.Client.LogOf(j.RemoteID) {
				out.Lines = append(out.Lines, line{At: l.At, Msg: l.Msg, From: "client"})
			}
		}
	}
	sort.SliceStable(out.Lines, func(i, k int) bool { return out.Lines[i].At.Before(out.Lines[k].At) })
	if len(out.Lines) > 300 {
		out.Lines = out.Lines[len(out.Lines)-300:]
	}
	writeJSON(w, out)
}
