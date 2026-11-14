// Package backend defines the download backends the pipeline can use.
package backend

import (
	"context"

	"github.com/z354prance/undermynd/internal/job"
)

type Status string

const (
	Downloading Status = "downloading"
	Complete    Status = "complete"
	Errored     Status = "error"
	Missing     Status = "missing"
)

type Item struct {
	Status   Status
	Progress float64
	Error    string
}

type File struct {
	Path string // relative to the job's save path
	Size int64
}

type Backend interface {
	Name() string
	// Add submits the job and returns the remote ID and the local path where its files will appear.
	Add(ctx context.Context, j job.Job) (remoteID, savePath string, err error)
	Status(ctx context.Context, remoteID string) (Item, error)
	Files(ctx context.Context, remoteID string) ([]File, error)
	// Remove deletes the remote item, with its data when deleteFiles is set.
	Remove(ctx context.Context, remoteID string, deleteFiles bool) error
}
