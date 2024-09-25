package jobs

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
)

type JobArgs map[string]any

// Job queue
type JobQueue interface {
	Use(JobMiddleware)
	AddQueue(string, int) error
	AddJobType(JobFn) error
	AddJob(context.Context, Job) error
	AddJobs(context.Context, []Job) error
	RunJob(context.Context, Job) error
	Run(context.Context) error
	Stop(context.Context) error
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

// JobWorker defines a job worker
type JobWorker interface {
	Kind() string
	Run(context.Context, Job) error
}

type JobFn func() JobWorker

type JobMiddleware func(JobWorker) JobWorker

///////////

type jobMapper struct {
	jobFns map[string]JobFn
}

func newJobMapper() *jobMapper {
	return &jobMapper{jobFns: map[string]JobFn{}}
}

func (j *jobMapper) AddJobType(jobFn JobFn) error {
	jw := jobFn()
	j.jobFns[jw.Kind()] = jobFn
	return nil
}

func (j *jobMapper) GetRunner(jobType string, jobArgs JobArgs) (JobWorker, error) {
	jobFn, ok := j.jobFns[jobType]
	if !ok {
		return nil, errors.New("unknown job type")
	}
	runner := jobFn()
	jw, err := json.Marshal(jobArgs)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(jw, runner); err != nil {
		return nil, err
	}
	return runner, nil
}
