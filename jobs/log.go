package jobs

import (
	"context"
	"time"

	"github.com/interline-io/log"
)

type jlog struct {
	JobWorker
}

func (w *jlog) Run(ctx context.Context, job Job) error {
	// Create logger for this job
	ctxLogger := log.For(ctx).With().Str("job_type", job.JobType).Str("job_id", job.jobId).Logger()

	// Attach to the context
	ctx = ctxLogger.WithContext(ctx)

	// Run next job
	t1 := time.Now()
	ctxLogger.Info().Msg("job: started")
	if err := w.JobWorker.Run(ctx, job); err != nil {
		ctxLogger.Error().Err(err).Msg("job: error")
		return err
	}
	ctxLogger.Info().Int64("job_time_ms", (time.Now().UnixNano()-t1.UnixNano())/1e6).Msg("job: completed")
	return nil

}

func newLog() JobMiddleware {
	return func(w JobWorker) JobWorker {
		return &jlog{JobWorker: w}
	}
}
