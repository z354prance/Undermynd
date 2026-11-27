package pipeline

import (
	"encoding/json"
	"sync"
	"time"
)

// Savings is the running total of what Tdarr has done to file sizes, kept
// after the jobs themselves are pruned.
type Savings struct {
	Files      int                     `json:"files"`
	Before     int64                   `json:"before"`
	After      int64                   `json:"after"`
	Since      time.Time               `json:"since"`
	Categories map[string]*SavingsPart `json:"categories"`
}

// SavingsPart is one category's share.
type SavingsPart struct {
	Files  int   `json:"files"`
	Before int64 `json:"before"`
	After  int64 `json:"after"`
}

const savingsBlob = "tdarr-savings.json"

var savingsMu sync.Mutex

func (e *Engine) loadSavings() Savings {
	sv := Savings{Categories: map[string]*SavingsPart{}}
	if e.Blobs != nil {
		if b, ok, err := e.Blobs.Load(savingsBlob); err == nil && ok {
			_ = json.Unmarshal(b, &sv)
		}
	} else if e.savingsMem != nil {
		b, _ := json.Marshal(e.savingsMem)
		_ = json.Unmarshal(b, &sv)
	}
	if sv.Categories == nil {
		sv.Categories = map[string]*SavingsPart{}
	}
	return sv
}

func (e *Engine) recordSavings(category string, before, after int64) {
	savingsMu.Lock()
	defer savingsMu.Unlock()
	sv := e.loadSavings()
	if sv.Since.IsZero() {
		sv.Since = e.Now()
	}
	sv.Files++
	sv.Before += before
	sv.After += after
	p := sv.Categories[category]
	if p == nil {
		p = &SavingsPart{}
		sv.Categories[category] = p
	}
	p.Files++
	p.Before += before
	p.After += after
	if e.Blobs != nil {
		b, _ := json.Marshal(sv)
		if err := e.Blobs.Save(savingsBlob, b); err != nil {
			e.Log.Printf("tdarr savings: %v", err)
		}
	} else {
		e.savingsMem = &sv
	}
}

// TdarrSavings is the running total.
func (e *Engine) TdarrSavings() Savings {
	savingsMu.Lock()
	defer savingsMu.Unlock()
	return e.loadSavings()
}
