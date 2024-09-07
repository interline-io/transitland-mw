package jobs

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
)

type JobArgs map[string]any

// Job queue
type JobQueue interface {
	AddJob(Job) error
	AddWorker(string, GetWorker, int) error
	Use(JobMiddleware)
	Run() error
	Stop() error
}

// Job defines a single job
type Job struct {
	Queue       string  `json:"queue"`
	JobType     string  `json:"job_type"`
	JobArgs     JobArgs `json:"job_args"`
	Unique      bool    `json:"unique"`
	JobDeadline int64   `json:"job_deadline"`
	jobId       string  `json:"-"`
}

func (job *Job) HexKey() (string, error) {
	bytes, err := json.Marshal(job.JobArgs)
	if err != nil {
		return "", err
	}
	sum := sha1.Sum(bytes)
	return job.JobType + ":" + hex.EncodeToString(sum[:]), nil
}

// GetWorker returns a new worker for this job type
type GetWorker func(Job) (JobWorker, error)

// JobWorker defines a job worker
type JobWorker interface {
	Run(context.Context, Job) error
}

type JobMiddleware func(JobWorker) JobWorker

////////////
