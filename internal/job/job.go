// Package job defines the per-grab job record and its state machine.
package job

import "time"

type State string

const (
	Received        State = "received"
	Submitted       State = "submitted"
	Downloading     State = "downloading"
	Pulling         State = "pulling"
	Downloaded      State = "downloaded"
	QueuedTranscode State = "queued_transcode"
	Transcoding     State = "transcoding"
	Verifying       State = "verifying"
	Ready           State = "ready"
	Imported        State = "imported"
	Cleaned         State = "cleaned"
	Failed          State = "failed"
)

type Transition struct {
	At     time.Time `json:"at"`
	From   State     `json:"from"`
	To     State     `json:"to"`
	Reason string    `json:"reason,omitempty"`
}

// Job is one grab, from the arr's hand-off to cleanup. ID is the lowercase
// torrent infohash (or the NZB ID for usenet), which is also the arr's download ID.
type Job struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Category string `json:"category"`
	Source   string `json:"source"` // radarr | sonarr | lidarr | rss
	Kind     string `json:"kind"`   // torrent | usenet

	State    State   `json:"state"`
	Reason   string  `json:"reason,omitempty"`
	Progress float64 `json:"progress"` // backend download progress, 0..1
	Size     int64   `json:"size"`

	Magnet      string `json:"magnet,omitempty"`
	TorrentPath string `json:"torrentPath,omitempty"`
	Backend     string `json:"backend,omitempty"`
	RemoteID    string `json:"remoteId,omitempty"`
	SavePath    string `json:"savePath,omitempty"`

	StagingDir  string   `json:"stagingDir,omitempty"`
	CompleteDir string   `json:"completeDir,omitempty"`
	Files       []string `json:"files,omitempty"` // staged, relative to StagingDir/CompleteDir

	Attempts int       `json:"attempts"` // retries inside the current state; reset on every transition
	Retries  int       `json:"retries"`  // re-submissions after a stall or client error; kept across states
	NextRun  time.Time `json:"nextRun,omitempty"`

	Created      time.Time `json:"created"`
	Updated      time.Time `json:"updated"`
	StateChanged time.Time `json:"stateChanged"`
	LastProgress time.Time `json:"lastProgress"`

	ArrDeleted  bool   `json:"arrDeleted,omitempty"`  // the arr called delete on this item
	FailCleaned bool   `json:"failCleaned,omitempty"` // files and backend item removed after a failure
	Flag        string `json:"flag,omitempty"`        // reconciler note shown in the UI

	History []Transition `json:"history,omitempty"`
}

// Move records a transition. It never changes the state to itself.
func (j *Job) Move(to State, reason string, now time.Time) {
	if j.State == to {
		j.Reason = reason
		j.Updated = now
		return
	}
	j.History = append(j.History, Transition{At: now, From: j.State, To: to, Reason: reason})
	if len(j.History) > 200 {
		j.History = j.History[len(j.History)-200:]
	}
	j.State = to
	j.Reason = reason
	j.StateChanged = now
	j.Updated = now
	j.Attempts = 0
	j.NextRun = time.Time{}
}

func (j Job) Clone() Job {
	c := j
	c.Files = append([]string(nil), j.Files...)
	c.History = append([]Transition(nil), j.History...)
	return c
}

// Active reports whether the pipeline still owns the job.
func (j Job) Active() bool { return j.State != Cleaned }

// Group is the coarse state shown on the public side.
func Group(s State) string {
	switch s {
	case Received, Submitted, QueuedTranscode:
		return "queued"
	case Downloading, Pulling, Downloaded:
		return "downloading"
	case Transcoding, Verifying:
		return "processing"
	case Ready, Imported:
		return "finishing"
	case Cleaned:
		return "done"
	case Failed:
		return "failed"
	}
	return "queued"
}

// Overall is the progress shown to the arr: the pipeline as a whole, never
// 100% until the file is in the complete folder.
func Overall(j Job) float64 {
	switch j.State {
	case Received, Submitted:
		return 0
	case Downloading, Pulling:
		return j.Progress * 0.9
	case Downloaded, QueuedTranscode, Transcoding, Verifying:
		return 0.95
	case Ready, Imported, Cleaned:
		return 1
	}
	return j.Progress * 0.9
}
